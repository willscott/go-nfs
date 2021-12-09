package main

import (
	"fmt"
	"net"
	"os"

	nfs "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/filesystem"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

func main() {
	port := ""
	if len(os.Args) < 2 {
		fmt.Printf("Usage: osnfs </path/to/folder> [port]\n")
		return
	} else if len(os.Args) == 3 {
		port = os.Args[2]
	}

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("Failed to listen: %v\n", err)
		return
	}
	fmt.Printf("osnfs server running at %s\n", listener.Addr())

	dfs := filesystem.NewWriteDirFSWrapper(os.Args[1])
	//dfs := os.DirFS(os.Args[1])

	handler := nfshelper.NewNullAuthHandler(dfs)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)
	fmt.Printf("%v", nfs.Serve(listener, cacheHelper))
}
