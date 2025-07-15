package main

// osview requires memphis, which does not yet support go-billy/v6

// import (
// 	"fmt"
// 	"net"
// 	"os"

// 	"github.com/willscott/memphis"

// 	nfs "github.com/willscott/go-nfs"
// 	nfshelper "github.com/willscott/go-nfs/helpers"
// )

// func main() {
// 	port := ""
// 	if len(os.Args) < 2 {
// 		fmt.Printf("Usage: osview </path/to/folder> [port]\n")
// 		return
// 	} else if len(os.Args) == 3 {
// 		port = os.Args[2]
// 	}

// 	listener, err := net.Listen("tcp", ":"+port)
// 	if err != nil {
// 		fmt.Printf("Failed to listen: %v\n", err)
// 		return
// 	}
// 	fmt.Printf("Server running at %s\n", listener.Addr())

// 	fs := memphis.FromOS(os.Args[1])
// 	bfs := fs.AsBillyFS(0, 0)

// 	handler := nfshelper.NewNullAuthHandler(bfs)
// 	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)
// 	fmt.Printf("%v", nfs.Serve(listener, cacheHelper))
// }
