package helpers

import (
	"context"
	"io/fs"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/filesystem"
)

// NewNullAuthHandler creates a handler for the provided filesystem
func NewNullAuthHandler(fs fs.FS) nfs.Handler {
	return &NullAuthHandler{fs}
}

// NullAuthHandler returns a NFS backing that exposes a given file system in response to all mount requests.
type NullAuthHandler struct {
	fs fs.FS
}

// Mount backs Mount RPC Requests, allowing for access control policies.
func (h *NullAuthHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl fs.FS, auths []nfs.AuthFlavor) {
	status = nfs.MountStatusOk
	hndl = h.fs
	auths = []nfs.AuthFlavor{nfs.AuthFlavorNull}
	return
}

// Change provides an interface for updating file attributes.
func (h *NullAuthHandler) Change(fs fs.FS) billy.Change {
	if c, ok := h.fs.(filesystem.Change); ok {
		return c
	}
	return nil
}

// FSStat provides information about a filesystem.
func (h *NullAuthHandler) FSStat(ctx context.Context, f fs.FS, s *nfs.FSStat) error {
	return nil
}

// ToHandle handled by CachingHandler
func (h *NullAuthHandler) ToHandle(f fs.FS, s []string) []byte {
	return []byte{}
}

// FromHandle handled by CachingHandler
func (h *NullAuthHandler) FromHandle([]byte) (fs.FS, []string, error) {
	return nil, []string{}, nil
}

// HandleLImit handled by cachingHandler
func (h *NullAuthHandler) HandleLimit() int {
	return -1
}
