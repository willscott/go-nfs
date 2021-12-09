package nfs

import (
	"context"
	"io/fs"
	"net"

	billy "github.com/go-git/go-billy/v5"
)

// Handler represents the interface of the file system / vfs being exposed over NFS
type Handler interface {
	// Required methods

	Mount(context.Context, net.Conn, MountRequest) (MountStatus, fs.FS, []AuthFlavor)

	// Change can return 'nil' if filesystem is read-only
	Change(fs.FS) billy.Change

	// Optional methods - generic helpers or trivial implementations can be sufficient depending on use case.

	// Fill in information about a file system's free space.
	FSStat(context.Context, fs.FS, *FSStat) error

	// represent file objects as opaque references
	// Can be safely implemented via helpers/cachinghandler.
	ToHandle(fs fs.FS, path []string) []byte
	FromHandle(fh []byte) (fs.FS, []string, error)
	// How many handles can be safely maintained by the handler.
	HandleLimit() int
}
