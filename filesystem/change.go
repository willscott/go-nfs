package filesystem

import (
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Change abstract the FileInfo change related operations in a storage-agnostic
// interface as an extension to the Basic interface
type Change interface {
	Chmod(name string, mode os.FileMode) error
	Lchown(name string, uid, gid int) error
	Chown(name string, uid, gid int) error
	Chtimes(name string, atime time.Time, mtime time.Time) error
}

type ChangeFS struct {
	fs.FS
}

func (c *ChangeFS) Open(name, uid string) {
}

func (c *ChangeFS) Chmod(name string, mode os.FileMode) error {
	if fs, ok := c.FS.(ChmodFS); ok {
		return fs.Chmod(name, mode)
	}

	return fmt.Errorf("chmod %s: operation not supported", name)
}

func (c *ChangeFS) Lchown(name string, uid, gid int) error {
	if fs, ok := c.FS.(LchownFS); ok {
		return fs.Lchown(name, uid, gid)
	}

	return fmt.Errorf("lchown %s: operation not supported", name)
}

func (c *ChangeFS) Chown(name string, uid, gid int) error {
	if fs, ok := c.FS.(ChownFS); ok {
		return fs.Chown(name, uid, gid)
	}

	return fmt.Errorf("chown %s: operation not supported", name)
}

func (c *ChangeFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if fs, ok := c.FS.(ChtimesFS); ok {
		return fs.Chtimes(name, atime, mtime)
	}

	return fmt.Errorf("chtimes %s: operation not supported", name)
}
