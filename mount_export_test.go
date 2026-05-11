package nfs_test

import (
	"io"
	"net"
	"testing"

	nfs "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"
	"github.com/willscott/go-nfs/helpers/memfs"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// readOpaque reads a variable-length opaque field and consumes the XDR
// 4-byte alignment padding. The go-nfs-client xdr.ReadOpaque helper does not
// consume the padding, which is fine for the last field of a body but breaks
// multi-field reads.
func readOpaque(r io.Reader) ([]byte, error) {
	length, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if pad := (4 - int(length)%4) % 4; pad > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(pad)); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

// readExportList reads the XDR-encoded reply body of a MOUNTPROC_EXPORT call
// and returns a flat list of (dirpath, groups). See RFC 1813 §5.2.5.
func readExportList(t *testing.T, r io.Reader) []nfs.Export {
	t.Helper()
	var out []nfs.Export
	for {
		more, err := xdr.ReadUint32(r)
		if err != nil {
			t.Fatalf("read exports value-follows: %v", err)
		}
		if more == 0 {
			return out
		}
		dir, err := readOpaque(r)
		if err != nil {
			t.Fatalf("read ex_dir: %v", err)
		}
		var groups []string
		for {
			more, err := xdr.ReadUint32(r)
			if err != nil {
				t.Fatalf("read group value-follows: %v", err)
			}
			if more == 0 {
				break
			}
			name, err := readOpaque(r)
			if err != nil {
				t.Fatalf("read group name: %v", err)
			}
			groups = append(groups, string(name))
		}
		out = append(out, nfs.Export{Dir: string(dir), Groups: groups})
	}
}

func callExport(t *testing.T, addr string) []nfs.Export {
	t.Helper()
	c, err := rpc.DialTCP("tcp", addr, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	res, err := c.Call(&rpc.Header{
		Rpcvers: 2,
		Prog:    nfsc.MountProg,
		Vers:    nfsc.MountVers,
		Proc:    nfsc.MountProc3Export,
		Cred:    rpc.AuthNull,
		Verf:    rpc.AuthNull,
	})
	if err != nil {
		t.Fatal(err)
	}
	return readExportList(t, res)
}

func startTestServer(t *testing.T, h nfs.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = nfs.Serve(listener, h) }()
	return listener.Addr().String()
}

func TestMountExportDefault(t *testing.T) {
	mem := memfs.New()
	f, _ := mem.Create("/test")
	_ = f.Close()

	handler := helpers.NewNullAuthHandler(mem)
	cached := helpers.NewCachingHandler(handler, 1024)

	exports := callExport(t, startTestServer(t, cached))

	if len(exports) != 1 {
		t.Fatalf("want 1 export, got %d (%+v)", len(exports), exports)
	}
	if exports[0].Dir != "/" {
		t.Errorf("want default dir \"/\", got %q", exports[0].Dir)
	}
	if len(exports[0].Groups) != 0 {
		t.Errorf("want no group restriction, got %v", exports[0].Groups)
	}
}

// exporterHandler wraps a Handler and implements the optional ExportHandler
// interface so MOUNTPROC_EXPORT returns a caller-supplied list instead of
// the default.
type exporterHandler struct {
	nfs.Handler
	list []nfs.Export
}

func (e *exporterHandler) Exports() []nfs.Export { return e.list }

func TestMountExportCustom(t *testing.T) {
	mem := memfs.New()
	f, _ := mem.Create("/test")
	_ = f.Close()

	base := helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), 1024)
	wrapped := &exporterHandler{
		Handler: base,
		list: []nfs.Export{
			{Dir: "/data", Groups: []string{"10.0.0.0/8", "192.168.0.0/16"}},
			{Dir: "/scratch"},
		},
	}

	got := callExport(t, startTestServer(t, wrapped))

	want := []nfs.Export{
		{Dir: "/data", Groups: []string{"10.0.0.0/8", "192.168.0.0/16"}},
		{Dir: "/scratch"},
	}
	if !exportsEqual(got, want) {
		t.Fatalf("export list mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func exportsEqual(a, b []nfs.Export) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Dir != b[i].Dir {
			return false
		}
		if len(a[i].Groups) != len(b[i].Groups) {
			return false
		}
		for j := range a[i].Groups {
			if a[i].Groups[j] != b[i].Groups[j] {
				return false
			}
		}
	}
	return true
}
