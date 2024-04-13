package file

import "os"

type FileInfo struct {
	Nlink  uint32
	UID    uint32
	GID    uint32
	Major  uint32
	Minor  uint32
	Fileid uint64
}

// GetInfo extracts some non-standardized items from the result of a Stat call.
func GetInfo(fi os.FileInfo) *FileInfo {
	sys := fi.Sys()
	if v, ok := sys.(*FileInfo); ok {
		return v
	}
	return getOSFileInfo(fi)
}
