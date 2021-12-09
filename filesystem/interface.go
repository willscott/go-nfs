package filesystem

import (
	"io/fs"
	"time"
)

// Interface for filesystems
type JoinFS interface {
	fs.FS
	Join(elem ...string) string
}

// inteface for filesystems that support writing
type WriteFileFS interface {
	fs.FS
	WriteFile(name string, data []byte, mode fs.FileMode) error
}

type MkdirAllFS interface {
	fs.FS
	MkdirAll(path string, perm fs.FileMode) error
}

type CreateFS interface {
	fs.FS
	Create(name string) (fs.File, error)
}

type OpenFileFS interface {
	fs.FS
	OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error)
}

type ChmodFS interface {
	fs.FS
	Chmod(name string, mode fs.FileMode) error
}

type LchownFS interface {
	fs.FS
	Lchown(name string, uid, gid int) error
}

type ChownFS interface {
	fs.FS
	Chown(name string, uid, gid int) error
}

type ChtimesFS interface {
	fs.FS
	Chtimes(name string, atime time.Time, mtime time.Time) error
}

type ReadlinkFS interface {
	fs.FS
	Readlink(name string) (string, error)
}

type RemoveFS interface {
	fs.FS
	Remove(name string) error
}

type RenameFS interface {
	fs.FS
	Rename(oldname string, newname string) error
}

type LstatFS interface {
	fs.FS
	Lstat(name string) (fs.FileInfo, error)
}

type SymlinkFS interface {
	fs.FS
	Symlink(oldname string, newname string) error
}

// Intefaces for file operations
type ReadAtFile interface {
	fs.File
	ReadAt(p []byte, off int64) (int, error)
}

type SeekFile interface {
	fs.File
	Seek(offset int64, whence int) (int64, error)
}

type WriteAtFile interface {
	fs.File
	WriteAt(p []byte, off int64) (int, error)
}

type TruncateFile interface {
	fs.File
	Truncate(size int64) error
}

type WriteFile interface {
	fs.File
	Write(b []byte) (int, error)
}
