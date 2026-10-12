package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"barely/internal/config"
	"barely/internal/diskimg"
	"barely/internal/guestimg"
	"barely/internal/launchd"
	"barely/internal/proto"
	"barely/internal/serve"
	"barely/internal/state"
	"barely/internal/vmtypes"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/term"
)

// setupFlags holds "barely setup"'s flags; newSetupFlags is also used by
// "barely help setup" and "barely man" so the flag list can never drift from
// what setup actually parses.
type setupFlags struct {
	fs            *flag.FlagSet
	context       *string
	selectContext *bool
	symlink       *string
	startAtLogin  *bool
	shellEnv      *bool
	memoryMax     *string
	diskMax       *string
	defaults      *bool
	from          *string
}

func newSetupFlags() *setupFlags {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	f := &setupFlags{fs: fs}
	f.context = fs.String("context", "barely", "Docker context name to create")
	f.selectContext = fs.Bool("select-context", true, "select the context as current (docker context use)")
	f.symlink = fs.String("symlink", "/var/run/docker.sock", "path to symlink to docker.sock (empty: skip this step)")
	f.startAtLogin = fs.Bool("start-at-login", false, "boot the VM at login (RunAtLoad)")
	f.shellEnv = fs.Bool("shell-env", false, "pin docker to barely in your shell profile with DOCKER_HOST")
	f.memoryMax = fs.String("memory-max", "", "memory cap for the VM, e.g. 8G (default: keep the current one)")
	f.diskMax = fs.String("disk-max", "", "disk cap for the VM, e.g. 100G; an existing disk can be raised, not lowered (default: keep the current one)")
	f.defaults = fs.Bool("defaults", false, "ask nothing: every choice not given as a flag takes its default")
	f.from = fs.String("from", "", "directory holding the signed barelyvm and libkrun.1.dylib to install (default: next to this barely, else ./out/bin)")
	return f
}

// setupUI asks at each choice that wasn't given as a flag when setup runs
// on a terminal; with -defaults (or no terminal) it asks nothing and every
// choice not given takes its default.
type setupUI struct {
	interactive bool
	given       map[string]bool
}

// asks reports whether the choice behind flag name should be put to the
// user: only when setup is interactive and the flag wasn't given.
func (ui *setupUI) asks(name string) bool {
	return ui.interactive && !ui.given[name]
}

func (ui *setupUI) ask(question string, def bool) bool {
	if !ui.interactive {
		return def
	}
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Printf("\n%s [%s] ", question, hint)
	var line string
	fmt.Scanln(&line)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

// askSize asks for a byte size, showing cur; an empty answer keeps it and
// an answer that doesn't parse or is below min is asked again.
func (ui *setupUI) askSize(question string, cur, min vmtypes.ByteSize) vmtypes.ByteSize {
	if !ui.interactive {
		return cur
	}
	for {
		fmt.Printf("\n%s [%s] ", question, sizeText(cur))
		line := readLine()
		if line == "" {
			return cur
		}
		v, err := parseCap(line)
		if err != nil {
			fmt.Printf("%q isn't a size; give one like 8G or 512M\n", line)
			continue
		}
		if v < min {
			fmt.Printf("%s is too small; the least it can be is %s\n", sizeText(v), sizeText(min))
			continue
		}
		return v
	}
}

// readLine reads one line from stdin a byte at a time, so nothing past the
// newline is consumed and the fmt.Scanln in ask still sees later answers.
func readLine() string {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if n == 0 || err != nil || buf[0] == '\n' {
			break
		}
		line = append(line, buf[0])
	}
	return strings.TrimSpace(string(line))
}

// parseCap parses a cap given to setup. A bare number means gigabytes: no
// cap is ever a number of bytes.
func parseCap(s string) (vmtypes.ByteSize, error) {
	s = strings.TrimSpace(s)
	if s != "" && strings.Trim(s, "0123456789.") == "" {
		s += "G"
	}
	return vmtypes.ParseByteSize(s)
}

func sizeText(b vmtypes.ByteSize) string {
	text, _ := b.MarshalText()
	return string(text)
}

// runSetup implements "barely setup". Output is one line per thing checked
// or changed; each question carries its own context.
func runSetup(args []string) error {
	flags := newSetupFlags()
	if err := flags.fs.Parse(args); err != nil {
		return err
	}
	ui := &setupUI{interactive: !*flags.defaults && term.IsTerminal(int(os.Stdin.Fd())), given: map[string]bool{}}
	flags.fs.Visit(func(f *flag.Flag) { ui.given[f.Name] = true })

	dir, err := state.Dir()
	if err != nil {
		return err
	}
	paths := state.New(dir)
	home, _ := os.UserHomeDir()
	short := func(p string) string { return strings.Replace(p, home, "~", 1) }

	if added, err := ensureBrewPluginDir(); err != nil {
		logLine("warn", "docker plugins", err.Error())
	} else if added {
		logLine("done", "docker plugins", "added "+brewPluginDir+" to "+short(dockerConfigPath()))
	}
	if printDeps(checkDeps()) {
		return fmt.Errorf("barely setup: install the missing pieces above and run setup again")
	}

	if err := state.EnsureDirs(dir); err != nil {
		return err
	}
	barelyPath, err := installBinaries(paths.BinDir(), *flags.from)
	if err != nil {
		return fmt.Errorf("barely setup: %w", err)
	}
	logLine("done", "binaries", short(paths.BinDir()))
	if _, err := installGuest(paths.GuestDir()); err != nil {
		return fmt.Errorf("barely setup: installing the guest image: %w", err)
	}
	img, err := guestimg.Find(paths.GuestDir(), proto.Version)
	if err != nil {
		return fmt.Errorf("barely setup: no usable guest image (%v); a release ships one, or build it with `make guest`", err)
	}
	logLine("done", "guest image", img.Manifest.Version)

	if err := setupCaps(ui, flags, paths); err != nil {
		return fmt.Errorf("barely setup: %w", err)
	}

	defaultDir, err := state.DefaultDir()
	if err != nil {
		return err
	}
	label := launchd.LabelForStateDir(dir, defaultDir)
	startAtLogin := *flags.startAtLogin
	if ui.asks("start-at-login") {
		startAtLogin = ui.ask("Boot the VM at login instead of on the first docker command?", false)
	}
	plistPath, err := launchd.WritePlist(label, barelyPath, dir, paths.DockerSock(), paths.ServeLog(), startAtLogin)
	if err != nil {
		return fmt.Errorf("barely setup: writing launchd plist: %w", err)
	}
	// Bootout first so re-running setup replaces an already-loaded agent
	// instead of failing on it.
	if err := launchd.Bootout(label); err != nil {
		return fmt.Errorf("barely setup: %w", err)
	}
	if err := launchd.Bootstrap(plistPath); err != nil {
		return fmt.Errorf("barely setup: %w", err)
	}
	atLogin := "on first docker command"
	if startAtLogin {
		atLogin = "at login"
	}
	logLine("done", "launchd agent", label+", VM starts "+atLogin)

	selectContext := *flags.selectContext
	if ui.asks("select-context") {
		selectContext = ui.ask(fmt.Sprintf("Make %q the current Docker context?", *flags.context), true)
	}
	if err := setupDockerContext(*flags.context, paths.DockerSock(), selectContext); err != nil {
		return fmt.Errorf("barely setup: %w", err)
	}
	current := "created, not selected"
	if selectContext {
		current = "created and selected"
	}
	logLine("done", "docker context", *flags.context+" "+current)

	if symlinkPath := *flags.symlink; symlinkPath != "" {
		linked, err := setupSymlink(ui, symlinkPath, paths.DockerSock())
		if err != nil {
			return fmt.Errorf("barely setup: %w", err)
		}
		if linked {
			logLine("done", "socket link", symlinkPath+" -> "+short(paths.DockerSock()))
		} else {
			logLine("skip", "socket link", symlinkPath+" unchanged")
		}
	}

	if profile, err := detectShellProfile(paths.DockerSock()); err != nil {
		if *flags.shellEnv {
			logLine("warn", "shell profile", err.Error())
		}
	} else {
		shellEnv := *flags.shellEnv
		if !ui.given["shell-env"] {
			shellEnv = ui.ask(fmt.Sprintf("Set DOCKER_HOST in %s so Docker Desktop can't switch docker away from barely?", short(profile.path)), fileExists(dockerDesktop))
		}
		if shellEnv {
			if err := profile.install(); err != nil {
				return fmt.Errorf("barely setup: %w", err)
			}
			logLine("done", "shell profile", "DOCKER_HOST set in "+short(profile.path))
		} else if removed, err := profile.remove(); err != nil {
			logLine("warn", "shell profile", err.Error())
		} else if removed {
			logLine("done", "shell profile", "DOCKER_HOST removed from "+short(profile.path))
		}
	}

	fmt.Println("\nsetup complete")
	return nil
}

// setupCaps lets setup choose the memory and disk caps. Only a cap that was
// changed is written to config.toml, so one left alone keeps following the
// default. Once data.img exists the disk cap can be raised (the guest grows
// the filesystem on its next boot) but not lowered.
func setupCaps(ui *setupUI, flags *setupFlags, paths state.Paths) error {
	cfg, err := config.Load(paths.Config())
	if err != nil {
		return err
	}
	changed := map[string]string{}

	memoryMax := cfg.MemoryMax
	if *flags.memoryMax != "" {
		if memoryMax, err = parseCap(*flags.memoryMax); err != nil {
			return fmt.Errorf("-memory-max: %w", err)
		}
	} else {
		memoryMax = ui.askSize("Memory cap for the VM? It only takes what containers use, up to this. Size like 8G or 512M.", cfg.MemoryMax, 1<<30)
	}
	if memoryMax != cfg.MemoryMax {
		cfg.MemoryMax = memoryMax
		changed["memory_max"] = sizeText(memoryMax)
	}

	// The disk as it is now: data.img's size once it exists (never below
	// the configured cap), else the configured cap.
	diskNow, question := cfg.DiskMax, "Disk cap for the VM? It only takes what images and volumes use, up to this. It can be raised later, not lowered."
	created := false
	if logical, _, err := diskimg.Usage(paths.DataImage()); err == nil && logical > 0 {
		created = true
		diskNow = max(diskNow, vmtypes.ByteSize(logical))
		question = "Disk cap for the VM? It can be raised, not lowered."
	}
	diskMax := diskNow
	if *flags.diskMax != "" {
		if diskMax, err = parseCap(*flags.diskMax); err != nil {
			return fmt.Errorf("-disk-max: %w", err)
		}
		if created && diskMax < diskNow {
			return fmt.Errorf("-disk-max: the disk is already %s; it can be raised, not lowered", sizeText(diskNow))
		}
	} else if created {
		diskMax = ui.askSize(question, diskNow, diskNow)
	} else {
		diskMax = ui.askSize(question, diskNow, 1<<30)
	}
	if diskMax != cfg.DiskMax {
		cfg.DiskMax = diskMax
		changed["disk_max"] = sizeText(diskMax)
	}

	if err := config.Validate(cfg); err != nil {
		return err
	}
	if err := writeConfigKeys(paths.Config(), changed); err != nil {
		return err
	}
	logLine("done", "memory cap", sizeText(cfg.MemoryMax))
	if created && diskMax > diskNow {
		logLine("done", "disk cap", sizeText(diskMax)+", grown from "+sizeText(diskNow)+" when the VM next starts")
	} else {
		logLine("done", "disk cap", sizeText(diskMax))
	}
	return nil
}

// writeConfigKeys sets keys in the config file at path, keeping whatever
// else is there, and creates the file if needed. With no keys it does
// nothing.
func writeConfigKeys(path string, keys map[string]string) error {
	if len(keys) == 0 {
		return nil
	}
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	for k, v := range keys {
		doc[k] = v
	}
	body, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// installBinaries copies this barely, the signed barelyvm and libkrun.1.dylib
// into binDir and returns the installed barely's path, which launchd runs.
// launchd then never depends on the repo's out/bin (rebuilt underneath
// running VMs) or on a temporary `go run` binary. Each file is written to a
// temp name and renamed into place, so a running VM keeps its old copy.
func installBinaries(binDir, from string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	if from == "" {
		from = firstDirWith(serve.HelperName, assetDirs(self, "libexec", "out/bin")...)
	}
	helper := filepath.Join(from, serve.HelperName)
	lib := filepath.Join(from, "libkrun.1.dylib")
	for _, p := range []string{helper, lib} {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s not found; build it with `make libkrun barelyvm` (or pass -from)", p)
		}
	}
	if err := serve.HasHypervisorEntitlement(helper); err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	installed := filepath.Join(binDir, "barely")
	for src, dst := range map[string]string{
		self:   installed,
		helper: filepath.Join(binDir, serve.HelperName),
		lib:    filepath.Join(binDir, "libkrun.1.dylib"),
	} {
		if src == dst {
			continue
		}
		if err := copyReplace(src, dst); err != nil {
			return "", err
		}
	}
	return installed, nil
}

// assetDirs lists where the files that ship with barely can be, in order: next
// to the running binary (an installed copy, or out/bin), the packaged
// layout beside it (<prefix>/bin/barely with <prefix>/<packaged>), and the
// repo's build output when run from a checkout.
func assetDirs(self, packaged, repo string) []string {
	dir := filepath.Dir(self)
	return []string{dir, filepath.Join(dir, "..", packaged), repo}
}

// firstDirWith returns the first of dirs containing name, else the last one so
// the caller's "not found" error names the repo build path.
func firstDirWith(name string, dirs ...string) string {
	for _, d := range dirs {
		if fileExists(filepath.Join(d, name)) {
			return d
		}
	}
	return dirs[len(dirs)-1]
}

// installGuest copies the newest guest image that ships with this barely into
// guestDir unless that version is already there, and returns the version
// (empty if no image ships alongside, as when only binaries were built).
func installGuest(guestDir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	newest, src := "", ""
	for _, d := range assetDirs(self, filepath.Join("share", "barely", "guest"), filepath.Join("out", "guest")) {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			// String comparison orders the version directory names.
			if e.IsDir() && e.Name() > newest && fileExists(filepath.Join(d, e.Name(), "manifest.json")) {
				newest, src = e.Name(), filepath.Join(d, e.Name())
			}
		}
	}
	if newest == "" || fileExists(filepath.Join(guestDir, newest, "manifest.json")) {
		return newest, nil
	}
	tmp := filepath.Join(guestDir, "."+newest+".new")
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := copyReplace(filepath.Join(src, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}
	return newest, os.Rename(tmp, filepath.Join(guestDir, newest))
}

// copyReplace writes dst through a temp file and a rename so a process that
// has the old dst open or mapped (a running VM) keeps its copy.
func copyReplace(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// setupDockerContext points a Docker context at barely's docker.sock. It is
// recreated on every run so a changed state directory can't leave a context
// aimed at the old socket.
func setupDockerContext(name, dockerSock string, selectIt bool) error {
	endpoint := "unix://" + dockerSock
	exec.Command("docker", "context", "rm", "-f", name).Run() // ignore: may not exist
	out, err := exec.Command("docker", "context", "create", name, "--docker", "host="+endpoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker context create: %w: %s", err, out)
	}
	if !selectIt {
		return nil
	}
	out, err = exec.Command("docker", "context", "use", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker context use: %w: %s", err, out)
	}
	return nil
}

// setupSymlink points symlinkPath at dockerSock and reports whether it
// did. Something already there that belongs to another tool is only
// replaced after asking, and never when setup isn't interactive.
func setupSymlink(ui *setupUI, symlinkPath, dockerSock string) (bool, error) {
	existing := ""
	if target, err := os.Readlink(symlinkPath); err == nil {
		if target == dockerSock {
			return true, nil
		}
		existing = "points to " + target
	} else if _, statErr := os.Lstat(symlinkPath); statErr == nil {
		existing = "exists and isn't a symlink"
	}
	switch {
	case existing != "":
		if !ui.ask(fmt.Sprintf("%s %s. Point it at barely instead (needs sudo)?", symlinkPath, existing), false) {
			return false, nil
		}
	case ui.asks("symlink"):
		if !ui.ask(fmt.Sprintf("Link %s to barely for tools that ignore the Docker context (needs sudo)?", symlinkPath), true) {
			return false, nil
		}
	}

	script := fmt.Sprintf("ln -sfn %s %s", shellQuote(dockerSock), shellQuote(symlinkPath))
	cmd := exec.Command("sudo", "sh", "-c", script)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("symlinking %s: %w", symlinkPath, err)
	}
	return true, nil
}

// shellQuote single-quotes s for the "sudo sh -c" script in setupSymlink,
// where the paths are interpolated into a command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
