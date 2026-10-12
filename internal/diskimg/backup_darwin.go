package diskimg

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// backupExcludeAttr is the extended attribute Time Machine looks for on a file
// it should skip, and backupExcludeValue what `tmutil addexclusion` stores in
// it: the string "com.apple.backupd" as a binary property list.
const backupExcludeAttr = "com.apple.metadata:com_apple_backup_excludeItem"

var backupExcludeValue = []byte("bplist00\x5f\x10\x11com.apple.backupd" +
	"\x08\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x01" +
	"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x1c")

// SetBackup includes path in Time Machine backups or leaves it out. The data
// disk is left out unless the user asks otherwise: it is one large file that
// changes whenever a container writes, so every backup would copy all of it
// again. The attribute is written directly rather than through tmutil, which
// takes a second per call.
func SetBackup(path string, include bool) error {
	if include {
		if err := unix.Removexattr(path, backupExcludeAttr); err != nil && !errors.Is(err, unix.ENOATTR) {
			return fmt.Errorf("diskimg: including %s in backups: %w", path, err)
		}
		return nil
	}
	if err := unix.Setxattr(path, backupExcludeAttr, backupExcludeValue, 0); err != nil {
		return fmt.Errorf("diskimg: leaving %s out of backups: %w", path, err)
	}
	return nil
}
