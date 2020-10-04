package main

import (
	"fmt"
	"net"
	"os"

	osfs "github.com/go-git/go-billy/v5/osfs"
	nfs "github.com/willscott/go-nfs"
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

	bfs := osfs.New(os.Args[1])
	bfsPlusChange := NewChangeOSFS(bfs)

	handler := nfshelper.NewNullAuthHandler(bfsPlusChange)
	cacheHelper := nfshelper.NewCachingHandler(handler)
	fmt.Printf("%v", nfs.Serve(listener, cacheHelper))
}
