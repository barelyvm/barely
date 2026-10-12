package config

import "golang.org/x/sys/unix"

func sysctlPhysMem() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
