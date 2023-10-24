//go:build darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris
// +build darwin dragonfly freebsd linux nacl netbsd openbsd solaris

package file

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func getInfo(info os.FileInfo) *FileInfo {
	fi := &FileInfo{}
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		fi.Nlink = uint32(s.Nlink)
		fi.UID = s.Uid
		fi.GID = s.Gid
		fi.Major = unix.Major(uint64(s.Rdev))
		fi.Minor = unix.Minor(uint64(s.Rdev))
		return fi
	}
	return nil
}
