package nfs_test

import (
	"net"
	"testing"

	nfs "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/go-git/go-billy/v5/memfs"
	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
)

func TestNFS(t *testing.T) {
	// make an empty in-memory server.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	mem := memfs.New()
	handler := helpers.NewNullAuthHandler(mem)
	cacheHelper := helpers.NewCachingHandler(handler)
	go nfs.Serve(listener, cacheHelper)

	c, err := rpc.DialTCP(listener.Addr().Network(), nil, listener.Addr().(*net.TCPAddr).String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/test", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	defer mounter.Unmount()

	_, err = target.FSInfo()
	if err != nil {
		t.Fatal(err)
	}
}
