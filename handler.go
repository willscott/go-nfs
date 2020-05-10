package nfs

import (
	"context"
	"net"

	billy "github.com/go-git/go-billy/v5"
)

// Handler represents the interface of the file system / vfs being exposed over NFS
type Handler interface {
	Mount(context.Context, net.Conn, MountRequest) (MountStatus, billy.Filesystem, []AuthFlavor)

	// represent file objects as opaque references
	ToHandle(fs billy.Filesystem, path string) []byte
	FromHandle(fh []byte) (billy.Filesystem, string, error)
}
