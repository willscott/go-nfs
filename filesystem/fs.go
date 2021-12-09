package filesystem

import (
	"fmt"
	"io/fs"
	iofs "io/fs"
	"path/filepath"
)

var ErrUnsupported = fmt.Errorf("unsupported operation")

// Methods that can be substituted by fs.
func Join(fs iofs.FS, elem ...string) string {
	if len(elem) == 0 {
		elem = []string{"."}
	}
	if FS, ok := fs.(JoinFS); ok {
		return FS.Join(elem...)
	}

	return filepath.Join(elem...)
}

func Stat(fs iofs.FS, filename string) (fs.FileInfo, error) {
	f, err := fs.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("Open: %w", err)
	}

	return f.Stat()
}

func ReadDir(fs iofs.FS, name string) ([]fs.DirEntry, error) {
	if FS, ok := fs.(iofs.ReadDirFS); ok {
		return FS.ReadDir(name)
	}

	return iofs.ReadDir(fs, name)
}

func MkdirAll(fs iofs.FS, path string, perm fs.FileMode) error {
	if FS, ok := fs.(MkdirAllFS); ok {
		return FS.MkdirAll(path, perm)
	}

	return fmt.Errorf("mkdirall %s: operation not supported", path)
}

func Create(fs iofs.FS, name string) (fs.File, error) {
	if FS, ok := fs.(CreateFS); ok {
		return FS.Create(name)
	}

	return nil, fmt.Errorf("create %s: operation not supported", name)
}

func Lstat(fs iofs.FS, name string) (fs.FileInfo, error) {
	if FS, ok := fs.(LstatFS); ok {
		return FS.Lstat(name)
	}

	return nil, fmt.Errorf("create %s: operation not supported", name)
}

func Rename(fs iofs.FS, oldpath, newpath string) error {
	if FS, ok := fs.(RenameFS); ok {
		return FS.Rename(oldpath, newpath)
	}

	return fmt.Errorf("rename %s to %s: operation not supported", oldpath, newpath)
}

func OpenFile(fs iofs.FS, name string, flag int, perm fs.FileMode) (fs.File, error) {
	if FS, ok := fs.(OpenFileFS); ok {
		return FS.OpenFile(name, flag, perm)
	}

	return nil, fmt.Errorf("openfile %s: operation not supported", name)
}

func Symlink(fs iofs.FS, oldname string, newname string) error {
	if FS, ok := fs.(SymlinkFS); ok {
		return FS.Symlink(oldname, newname)
	}

	return fmt.Errorf("symlink %s to %s: operation not supported", oldname, newname)
}

func Readlink(fs iofs.FS, name string) (string, error) {
	if FS, ok := fs.(ReadlinkFS); ok {
		return FS.Readlink(name)
	}

	return "", fmt.Errorf("readlink %s: operation not supported", name)
}

func Remove(fs iofs.FS, name string) error {
	if FS, ok := fs.(RemoveFS); ok {
		return FS.Remove(name)
	}

	return fmt.Errorf("remove %s: operation not supported", name)
}

func WriteCapabilityCheck(fs iofs.FS) bool {
	// MkdirAll is required for a writable NFS mount
	_, ok := fs.(MkdirAllFS)
	return ok
}
