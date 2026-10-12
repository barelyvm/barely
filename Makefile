BARELY_HOME ?= $(HOME)/.barely
.PHONY: kernel kernel-config kernel-clean libkrun barely barelyvm agent guest guest-image man release pack formula push-release push-formula

OUT := out/bin
LIBCLANG := $(shell xcode-select -p)/Toolchains/XcodeDefault.xctoolchain/usr/lib
CARGO_ENV := PATH=/opt/homebrew/opt/rustup/bin:/opt/homebrew/opt/lld/bin:$$PATH LIBCLANG_PATH=$(LIBCLANG)

# Guest image.
# The second line freezes the timestamp: `?=` alone is still a recursive
# variable, so every $(GUEST_VERSION) reference below would re-run `date`
# and the guest target and timestamp would drift apart mid-build.
GUEST_VERSION ?= $(shell date -u +%Y.%m.%d.%H%M)
GUEST_VERSION := $(GUEST_VERSION)
GUEST_BUILD_IMAGE := barely-guest-build
GUEST_OUT := out/guest/$(GUEST_VERSION)
KERNEL_VERSION := $(shell . kernel/version.env && echo $$KERNEL_VERSION)
AGENT_PROTOCOL := 3
FSEVENT_ABI := 1

# Release. VERSION is what `barely version` prints and what a release is
# named: the nearest tag without its "v", or the commit when there is none.
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty))
DIST := dist
RELEASE := barely-$(VERSION)-darwin-arm64
GITHUB_REPO := barelyvm/barely
# The branch a release is built from.
RELEASE_SOURCE ?= https://github.com/$(GITHUB_REPO).git
RELEASE_REF ?= main
# A local clone of github.com/barelyvm/homebrew-tap, for push-formula.
TAP_DIR ?= ../homebrew-tap
# Where the formula downloads the tarball from; by default, the local build.
RELEASE_URL ?= file://$(CURDIR)/$(DIST)/$(RELEASE).tar.gz

kernel kernel-config kernel-clean:
	$(MAKE) -C kernel $@

# Builds barely's libkrun fork and ships the dylib next to the barely binary,
# found at runtime via rpath, plus the header internal/krun compiles against.
# The fork is fetched into out/libkrun at the commit libkrun.env pins. Set
# LIBKRUN_DIR to build a working copy instead: whatever is checked out there
# is built, so the last line says which commit that was.
include libkrun.env
LIBKRUN_DIR ?= out/libkrun
libkrun:
ifeq ($(LIBKRUN_DIR),out/libkrun)
	test -d out/libkrun/.git || git clone -q $(LIBKRUN_URL) out/libkrun
	git -C out/libkrun cat-file -e '$(LIBKRUN_COMMIT)^{commit}' 2>/dev/null || git -C out/libkrun fetch -q $(LIBKRUN_URL) $(LIBKRUN_COMMIT)
	git -C out/libkrun checkout -q --detach $(LIBKRUN_COMMIT)
endif
	cd $(LIBKRUN_DIR) && $(CARGO_ENV) $(MAKE) BLK=1 NET=1
	mkdir -p $(OUT) out/include
	cp $(LIBKRUN_DIR)/include/libkrun.h out/include/libkrun.h
	cp $(LIBKRUN_DIR)/target/release/libkrun.dylib $(OUT)/libkrun.1.dylib
	install_name_tool -id @rpath/libkrun.1.dylib $(OUT)/libkrun.1.dylib
	codesign -s - -f $(OUT)/libkrun.1.dylib
	@echo "libkrun: built $$(git -C $(LIBKRUN_DIR) rev-parse HEAD) from $(LIBKRUN_DIR); libkrun.env pins $(LIBKRUN_COMMIT)"

# Builds the barely binary (host process and CLI).
# barely needs no signing: only barelyvm needs the hypervisor entitlement.
barely:
	mkdir -p $(OUT)
	go build -ldflags "-X main.version=$(VERSION)" -o $(OUT)/barely ./cmd/barely

# barelyvm runs the VM; libkrun.1.dylib is found next to it (@executable_path).
barelyvm:
	mkdir -p $(OUT)
	go build -ldflags '-extldflags "-Wl,-rpath,@executable_path"' -o $(OUT)/barelyvm ./cmd/barelyvm
	codesign -s - --entitlements barely.entitlements --force $(OUT)/barelyvm

# Generates the barely(1) man page from the help text in cmd/barely.
man: barely
	mkdir -p out/man
	$(OUT)/barely man -o out/man/barely.1

# Builds barely-agent for the guest (linux/arm64, no cgo: it only uses vsock and
# the mount/network syscalls, both pure Go). AGENT_PROTOCOL must match
# internal/proto.Version.
agent:
	mkdir -p out/guest-build/agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(GUEST_VERSION)" \
		-o out/guest-build/agent/barely-agent ./cmd/barely-agent

# Builds the guest image (kernel, Alpine root.img, initrd.img carrying
# barely-agent as /init, manifest.json) into out/guest/<version>/.
guest-image: kernel agent
	docker build --platform linux/arm64 -q -t $(GUEST_BUILD_IMAGE) guest
	mkdir -p $(GUEST_OUT)
	cp kernel/out/Image $(GUEST_OUT)/Image
	docker run --rm --platform linux/arm64 \
		-v $(CURDIR)/guest:/guest:ro \
		-v $(CURDIR)/out/guest-build:/build \
		-v $(CURDIR)/$(GUEST_OUT):/out \
		$(GUEST_BUILD_IMAGE) sh /guest/build.sh "$(GUEST_VERSION)" "$(KERNEL_VERSION)" "$(AGENT_PROTOCOL)" "$(FSEVENT_ABI)"

# Builds the guest image and installs it to ~/.barely/guest/.
guest: guest-image
	mkdir -p $(BARELY_HOME)/guest
	rm -rf $(BARELY_HOME)/guest/$(GUEST_VERSION)
	cp -R $(GUEST_OUT) $(BARELY_HOME)/guest/$(GUEST_VERSION)
	@echo "guest $(GUEST_VERSION) installed to $(BARELY_HOME)/guest/$(GUEST_VERSION)"

# Builds dist/barely-<version>-darwin-arm64.tar.gz from a fresh clone of
# RELEASE_REF, kept in dist/src/<commit>. Uploads nothing.
# Usage: make release VERSION=0.1.0
release:
	@test "$(origin VERSION)" = "command line" || { echo "give the version: make release VERSION=0.1.0"; exit 1; }
	mkdir -p $(DIST)/src
	commit=$$(git ls-remote $(RELEASE_SOURCE) refs/heads/$(RELEASE_REF) | cut -f1) && test -n "$$commit" && \
	src=$(CURDIR)/$(DIST)/src/$$commit && \
	{ test -d $$src || git clone -q --depth 1 --branch $(RELEASE_REF) $(RELEASE_SOURCE) $$src; } && \
	test "$$(git -C $$src rev-parse HEAD)" = "$$commit" && \
	test -z "$$(git -C $$src status --porcelain)" && \
	$(MAKE) -C $$src pack VERSION=$(VERSION) DIST=$(CURDIR)/$(DIST) && \
	echo $$commit > $(DIST)/$(RELEASE).commit
	@echo "release: $(DIST)/$(RELEASE).tar.gz, built from $$(cat $(DIST)/$(RELEASE).commit)"

# Builds this folder and packs it into $(DIST): bin/barely, libexec/ (barelyvm,
# libkrun, entitlements) and share/barely/guest/<guest version>/, the layout
# `barely setup` looks for. Each -s places one built file in the archive.
pack: libkrun barely barelyvm guest-image
	mkdir -p $(DIST)
	tar -czf $(DIST)/$(RELEASE).tar.gz \
		-s '|^$(OUT)/barely$$|$(RELEASE)/bin/barely|' \
		-s '|^$(OUT)/|$(RELEASE)/libexec/|' \
		-s '|^barely.entitlements$$|$(RELEASE)/libexec/barely.entitlements|' \
		-s '|^$(GUEST_OUT)|$(RELEASE)/share/barely/guest/$(GUEST_VERSION)|' \
		-s '|^LICENSE$$|$(RELEASE)/LICENSE|' \
		-s '|^README.md$$|$(RELEASE)/README.md|' \
		$(OUT)/barely $(OUT)/barelyvm $(OUT)/libkrun.1.dylib barely.entitlements \
		$(GUEST_OUT) LICENSE README.md
	cd $(DIST) && shasum -a 256 $(RELEASE).tar.gz > $(RELEASE).tar.gz.sha256

# Writes dist/barely.rb, the Homebrew formula for the tarball `make release`
# built with the same VERSION.
formula:
	test -f $(DIST)/$(RELEASE).tar.gz.sha256
	sed -e 's|@URL@|$(RELEASE_URL)|' -e 's|@VERSION@|$(VERSION)|' \
		-e "s|@SHA256@|$$(cut -d' ' -f1 $(DIST)/$(RELEASE).tar.gz.sha256)|" \
		packaging/homebrew/barely.rb.in > $(DIST)/barely.rb
	@echo "formula: $(DIST)/barely.rb"

# Uploads the tarball `make release` built with the same VERSION as a draft
# GitHub release v<version>, then rewrites dist/barely.rb to download from
# it. Publishing the draft on GitHub creates the tag, on the commit the
# tarball was built from.
push-release:
	@test -f $(DIST)/$(RELEASE).tar.gz.sha256
	@test -f $(DIST)/$(RELEASE).commit
	@sed -e 's|@VERSION@|$(VERSION)|g' -e 's|@KERNEL@|$(KERNEL_VERSION)|g' \
		packaging/release-notes.md.in > $(DIST)/release-notes.md
	@gh release create v$(VERSION) --repo $(GITHUB_REPO) --target $$(cat $(DIST)/$(RELEASE).commit) --draft \
		--title "barely $(VERSION)" --notes-file $(DIST)/release-notes.md --generate-notes \
		$(DIST)/$(RELEASE).tar.gz $(DIST)/$(RELEASE).tar.gz.sha256 > $(DIST)/release-url
	@$(MAKE) -s formula VERSION=$(VERSION) \
		RELEASE_URL=https://github.com/$(GITHUB_REPO)/releases/download/v$(VERSION)/$(RELEASE).tar.gz
	@echo
	@echo "Draft release v$(VERSION):"
	@echo
	@echo "    $$(cat $(DIST)/release-url)"
	@echo
	@echo "Publish it there, then run: make push-formula VERSION=$(VERSION)"
	@echo

# Commits dist/barely.rb to the Homebrew tap and pushes it. Run after the
# draft release is published: the formula's URL does not exist before then.
push-formula:
	@test -d $(TAP_DIR)/.git || { echo "no clone of the tap at $(TAP_DIR); set TAP_DIR"; exit 1; }
	@grep -q 'releases/download/v$(VERSION)/' $(DIST)/barely.rb || { echo "$(DIST)/barely.rb is not for the v$(VERSION) GitHub release; run make push-release VERSION=$(VERSION)"; exit 1; }
	@test "$$(gh release view v$(VERSION) --repo $(GITHUB_REPO) --json isDraft --jq .isDraft)" = false || { echo "release v$(VERSION) is still a draft (or missing); publish it on GitHub first"; exit 1; }
	mkdir -p $(TAP_DIR)/Formula
	cp $(DIST)/barely.rb $(TAP_DIR)/Formula/barely.rb
	git -C $(TAP_DIR) add Formula/barely.rb
	git -C $(TAP_DIR) commit -m "barely $(VERSION)"
	git -C $(TAP_DIR) push origin HEAD
