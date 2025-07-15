package main

import (
	"fmt"
	"net"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"

	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

// ROFS is an intercepter for the filesystem indicating it should
// be read only. The undelrying billy.Memfs indicates it supports
// writing, but does not in implement billy.Change to support
// modification of permissions / modTimes, and as such cannot be
// used as RW system.
type ROFS struct {
	billy.Filesystem
}

// Capabilities exports the filesystem as readonly
func (ROFS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}

func main() {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Printf("Failed to listen: %v\n", err)
		return
	}
	fmt.Printf("Server running at %s\n", listener.Addr())

	mem := memfs.New()
	f, err := mem.Create("hello.txt")
	if err != nil {
		fmt.Printf("Failed to create file: %v\n", err)
		return
	}
	_, _ = f.Write([]byte("hello world"))
	_ = f.Close()

	handler := nfshelper.NewNullAuthHandler(ROFS{mem})
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)
	fmt.Printf("%v", nfs.Serve(listener, cacheHelper))
}
