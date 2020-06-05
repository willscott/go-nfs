package nfs_test

import (
	"bytes"
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
	// File needs to exist in the root for memfs to acknowledge the root exists.
	_, _ = mem.Create("/test")

	handler := helpers.NewNullAuthHandler(mem)
	cacheHelper := helpers.NewCachingHandler(handler)
	go func() {
		_ = nfs.Serve(listener, cacheHelper)
	}()

	c, err := rpc.DialTCP(listener.Addr().Network(), nil, listener.Addr().(*net.TCPAddr).String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = mounter.Unmount()
	}()

	_, err = target.FSInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Validate sample file creation
	_, err = target.Create("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := mem.Stat("/helloworld.txt"); err != nil {
		t.Fatal(err)
	} else {
		if info.Size() != 0 || info.Mode().Perm() != 0666 {
			t.Fatal("incorrect creation.")
		}
	}

	// Validate writing to a file.
	f, err := target.OpenFile("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	b := []byte("hello world")
	_, err = f.Write(b)
	if err != nil {
		t.Fatal(err)
	}
	mf, _ := mem.Open("/helloworld.txt")
	buf := make([]byte, len(b))
	if _, err = mf.Read(buf[:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, b) {
		t.Fatal("written does not match expected")
	}
}
