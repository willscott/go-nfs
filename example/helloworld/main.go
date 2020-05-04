package main

import (
	"context"
	"fmt"
	"net"

	nfs "github.com/willscott/go-nfs"
)

// HelloWorldHandler Provides an unauthenticated simple backing of a single static file.
type HelloWorldHandler struct{}

// Mount backs Mount RPC Requests, allowing for access control policies.
func (h *HelloWorldHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl nfs.FileHandle, auths []nfs.AuthFlavor) {
	status = nfs.MountStatusOk
	hndl = req.Dirpath
	auths = []nfs.AuthFlavor{nfs.AuthFlavorNull}
	return
}

func main() {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Printf("Failed to listen: %v\n", err)
		return
	}
	handler := HelloWorldHandler{}
	fmt.Printf("Server running at %s\n", listener.Addr())
	nfs.Serve(listener, &handler)
}
