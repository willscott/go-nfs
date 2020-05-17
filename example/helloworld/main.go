package main

import (
	"context"
	"fmt"
	"net"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"

	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

// HelloWorldHandler Provides an unauthenticated simple backing of a single static file.
type HelloWorldHandler struct {
	fs billy.Filesystem
}

// Mount backs Mount RPC Requests, allowing for access control policies.
func (h *HelloWorldHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl billy.Filesystem, auths []nfs.AuthFlavor) {
	status = nfs.MountStatusOk
	hndl = h.fs
	auths = []nfs.AuthFlavor{nfs.AuthFlavorNull}
	return
}

// FSStat provides information about a filesystem.
func (h *HelloWorldHandler) FSStat(ctx context.Context, f billy.Filesystem, s *nfs.FSStat) error {
	return nil
}

// ToHandle handled by CachingHandler
func (h *HelloWorldHandler) ToHandle(f billy.Filesystem, s []string) []byte {
	return []byte{}
}

// FromHandle handled by CachingHandler
func (h *HelloWorldHandler) FromHandle([]byte) (billy.Filesystem, []string, error) {
	return nil, []string{}, nil
}

func main() {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Printf("Failed to listen: %v\n", err)
		return
	}
	handler := HelloWorldHandler{}
	fmt.Printf("Server running at %s\n", listener.Addr())

	mem := memfs.New()
	f, err := mem.Create("hello.txt")
	f.Write([]byte("hello world"))
	f.Close()

	handler.fs = mem
	cacheHelper := nfshelper.NewCachingHandler(&handler)
	nfs.Serve(listener, cacheHelper)
}
