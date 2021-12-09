package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// NewWriteDirFSWrapper creates a new filesystem wrapper with write functionality.
func NewWriteDirFSWrapper(root string) fs.FS {
	return &writeDirFS{os.DirFS(root), root}
}

// NewWriteDirFSWithChangeWrapper includes methods for updating file attributes.
func NewWriteDirFSWithChangeWrapper(root string) fs.FS {
	dfs := NewWriteDirFSWrapper(root)
	dfsc := struct {
		fs.FS
		*writeDirFSChange
	}{
		dfs,
		&writeDirFSChange{root},
	}
	return dfsc
}

type writeDirFS struct {
	fs.FS
	root string
}

func (dfs *writeDirFS) Open(name string) (fs.File, error) {
	file, err := dfs.FS.Open(name)
	if err != nil {
		return nil, err
	}

	return NewDirFileWrapper(dfs, file, dfs.root)
}

func (dfs *writeDirFS) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (dfs *writeDirFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(dfs.Join(dfs.root, name))
}

func (dfs *writeDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(dfs.Join(dfs.root, name))
}

func (dfs *writeDirFS) WriteFile(name string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(dfs.Join(dfs.root, name), data, mode)
}

func (dfs *writeDirFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(dfs.Join(dfs.root, path), perm)
}

func (dfs *writeDirFS) Create(name string) (fs.File, error) {
	file, err := os.Create(dfs.Join(dfs.root, name))
	if err != nil {
		return nil, err
	}

	return NewDirFileWrapper(dfs, file, dfs.root)
}

func (dfs *writeDirFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
	file, err := os.OpenFile(dfs.Join(dfs.root, name), flag, perm)
	if err != nil {
		return nil, err
	}

	return NewDirFileWrapper(dfs, file, dfs.root)
}

func (dfs *writeDirFS) Readlink(name string) (string, error) {
	return os.Readlink(dfs.Join(dfs.root, name))
}

func (dfs *writeDirFS) Remove(name string) error {
	return os.Remove(dfs.Join(dfs.root, name))
}

func (dfs *writeDirFS) Rename(oldname, newname string) error {
	return os.Rename(dfs.Join(dfs.root, oldname), dfs.Join(dfs.root, newname))
}

func (dfs *writeDirFS) Lstat(name string) (os.FileInfo, error) {
	return os.Lstat(dfs.Join(dfs.root, name))
}

func (dfs *writeDirFS) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, dfs.Join(dfs.root, newname))
}

func (dfs *writeDirFS) RemoveAll(path string) error {
	return os.RemoveAll(dfs.Join(dfs.root, path))
}

// Change attribute implementations
type writeDirFSChange struct {
	root string
}

func (dfs *writeDirFSChange) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (dfsc *writeDirFSChange) Chmod(name string, mode fs.FileMode) error {
	return os.Chmod(dfsc.Join(dfsc.root, name), mode)
}

func (dfsc *writeDirFSChange) Lchown(name string, uid int, gid int) error {
	return os.Lchown(dfsc.Join(dfsc.root, name), uid, gid)
}

func (dfsc *writeDirFSChange) Chown(name string, uid int, gid int) error {
	return os.Chown(dfsc.Join(dfsc.root, name), uid, gid)
}

func (dfsc *writeDirFSChange) Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(dfsc.Join(dfsc.root, name), atime, mtime)
}

// Create new file wrapper with write functionality
func NewDirFileWrapper(fs *writeDirFS, file fs.File, root string) (fs.File, error) {
	if osFile, ok := file.(*os.File); ok {
		return &writeDirFile{osFile, fs.Join(root, osFile.Name())}, nil
	}

	return nil, errors.New("Not an os.File")
}

type writeDirFile struct {
	fs.File
	path string
}

func (df *writeDirFile) ReadAt(b []byte, off int64) (int, error) {
	if osFile, ok := df.File.(*os.File); ok {
		return osFile.ReadAt(b, off)
	}
	return 0, errors.New("Not an os.File")
}

func (df *writeDirFile) Seek(offset int64, whence int) (int64, error) {
	if osFile, ok := df.File.(*os.File); ok {
		return osFile.Seek(offset, whence)
	}
	return 0, errors.New("Not an os.File")
}

func (df *writeDirFile) WriteAt(p []byte, off int64) (int, error) {
	if osFile, ok := df.File.(*os.File); ok {
		return osFile.WriteAt(p, off)
	}
	return 0, errors.New("Not an os.File")
}

func (df *writeDirFile) Truncate(size int64) error {
	if osFile, ok := df.File.(*os.File); ok {
		return osFile.Truncate(size)
	}
	return errors.New("Not an os.File")
}

func (df *writeDirFile) Write(b []byte) (int, error) {
	if osFile, ok := df.File.(*os.File); ok {
		return osFile.Write(b)
	}
	return 0, errors.New("Not an os.File")
}
