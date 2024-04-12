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

// FileInfoGetter allows os.FileInfo implementations that implement
// the GetFileInfo() method to explicitly return a *FileInfo.
// Useful for explicitly setting a Fileid without having to use the syscall package
type FileInfoGetter interface {
	GetFileInfo() *FileInfo
}

// GetInfo extracts some non-standardized items from the result of a Stat call.
func GetInfo(fi os.FileInfo) *FileInfo {
	if v, ok := fi.(FileInfoGetter); ok {
		return v.GetFileInfo()
	}
	return getOSFileInfo(fi)
}
