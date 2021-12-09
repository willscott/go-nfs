package main

import (
	"fmt"
	"net"

	"github.com/psanford/memfs"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

func main() {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Printf("Failed to listen: %v\n", err)
		return
	}
	fmt.Printf("Server running at %s\n", listener.Addr())

	mem := memfs.New()
	err = mem.WriteFile("hello.txt", []byte("hello world"), 0755)
	if err != nil {
		fmt.Printf("Failed to create file: %v\n", err)
		return
	}

	handler := nfshelper.NewNullAuthHandler(mem)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)
	fmt.Printf("%v", nfs.Serve(listener, cacheHelper))
}
