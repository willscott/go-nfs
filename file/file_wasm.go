//go:build wasm

package file

import (
	"os"
	"syscall"
)

func getOSFileInfo(info os.FileInfo) *FileInfo {
	fi := &FileInfo{}
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		fi.Nlink = uint32(s.Nlink)
		fi.UID = s.Uid
		fi.GID = s.Gid
		fi.Fileid = s.Ino
		return fi
	}
	return nil
}
