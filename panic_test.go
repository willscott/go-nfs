package nfs_test

import (
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"

	nfs "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"
	"github.com/willscott/go-nfs/helpers/memfs"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
)

type panickingFS struct {
	billy.Filesystem
	armed atomic.Bool
}

func (p *panickingFS) ReadDir(path string) ([]os.FileInfo, error) {
	if p.armed.Load() {
		panic("boom")
	}
	return p.Filesystem.ReadDir(path)
}

func TestPanicHandlerRecovers(t *testing.T) {
	fs := &panickingFS{Filesystem: memfs.New()}
	if f, err := fs.Filesystem.Create("/seed"); err != nil {
		t.Fatal(err)
	} else {
		_ = f.Close()
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var recovered atomic.Value
	srv := &nfs.Server{
		Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(fs), 1024),
		PanicHandler: func(r any) nfs.ResponseCode {
			recovered.Store(r)
			return nfs.ResponseCodeSystemErr
		},
	}
	go func() { _ = srv.Serve(listener) }()

	c, err := rpc.DialTCP(listener.Addr().Network(), listener.Addr().String(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mounter := nfsc.Mount{Client: c}
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	defer mounter.Unmount()

	if _, err := target.ReadDirPlus("/"); err != nil {
		t.Fatalf("pre-panic ReadDirPlus failed: %v", err)
	}

	fs.armed.Store(true)
	if _, err := target.ReadDirPlus("/"); err == nil {
		t.Fatal("expected panic to surface as a request error, got nil")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := recovered.Load(); v != nil {
			s, ok := v.(string)
			if !ok || !strings.Contains(s, "boom") {
				t.Fatalf("unexpected recovered value: %#v", v)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if recovered.Load() == nil {
		t.Fatal("PanicHandler was not invoked")
	}

	fs.armed.Store(false)
	if _, err := target.ReadDirPlus("/"); err != nil {
		t.Fatalf("post-panic ReadDirPlus failed: %v", err)
	}
}

func TestPanicHandlerNilIsDefault(t *testing.T) {
	if (&nfs.Server{}).PanicHandler != nil {
		t.Fatal("PanicHandler should default to nil")
	}
}
