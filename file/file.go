package file

import (
	"os"
	"time"
)

type FileInfo struct {
	Nlink  uint32
	UID    uint32
	GID    uint32
	Major  uint32
	Minor  uint32
	Fileid uint64
	Atime  time.Time
	Ctime  time.Time
}

// GetInfo extracts some non-standardized items from the result of a Stat call.
func GetInfo(fi os.FileInfo) *FileInfo {
	sys := fi.Sys()
	switch v := sys.(type) {
	case FileInfo:
		return &v
	case *FileInfo:
		return v
	default:
		return getOSFileInfo(fi)
	}
}
