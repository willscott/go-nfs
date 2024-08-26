//go:build darwin

package file

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func getOSFileInfo(info os.FileInfo) *FileInfo {
	fi := &FileInfo{}
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		fi.Nlink = uint32(s.Nlink)
		fi.UID = s.Uid
		fi.GID = s.Gid
		fi.Major = unix.Major(uint64(s.Rdev))
		fi.Minor = unix.Minor(uint64(s.Rdev))
		fi.Fileid = s.Ino
		fi.Atime = time.Unix(s.Atimespec.Sec, s.Atimespec.Nsec)
		fi.Ctime = time.Unix(s.Ctimespec.Sec, s.Ctimespec.Nsec)
		return fi
	}
	return nil
}
