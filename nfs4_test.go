package nfs

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs/helpers/memfs"
)

func TestNFSv4CompoundPutRootFHGetAttr(t *testing.T) {
	fs := memfs.New()
	if err := fs.MkdirAll("/", 0755); err != nil {
		t.Fatalf("failed to create test root: %v", err)
	}
	handler := newNFSv4TestHandler(fs)
	srv := &Server{
		Handler: handler,
		ID:      [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
	}

	reqBody := bytes.NewBuffer(nil)
	req := newNFS4Writer(reqBody)
	req.writeOpaque([]byte("root"))
	req.writeUint32(0) // minorversion
	req.writeUint32(3) // op count
	req.writeUint32(uint32(opPutRootFH))
	req.writeUint32(uint32(opGetFH))
	req.writeUint32(uint32(opGetAttr))
	writeBitmap(req, bitmapFromAttrs(fattr4Type, fattr4Mode))

	w := &response{
		conn: &conn{Server: srv},
		req: &request{
			xid:  1,
			Body: bytes.NewReader(reqBody.Bytes()),
		},
		errorFmt: basicErrorFormatter,
		writer:   bytes.NewBuffer(nil),
	}

	if err := onNFSv4Compound(context.Background(), w, handler); err != nil {
		t.Fatalf("onNFSv4Compound returned error: %v", err)
	}

	resp := newNFS4Reader(bytes.NewReader(w.writer.Bytes()))
	if xid, err := resp.readUint32(); err != nil || xid != 1 {
		t.Fatalf("xid = %d, %v; want 1, nil", xid, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := resp.readUint32(); err != nil {
			t.Fatalf("failed to read RPC reply header word %d: %v", i, err)
		}
	}
	if _, err := resp.readOpaque(nfs4OpaqueLimit); err != nil {
		t.Fatalf("failed to read RPC verifier: %v", err)
	}
	if acceptStatus, err := resp.readUint32(); err != nil || acceptStatus != uint32(ResponseCodeSuccess) {
		t.Fatalf("accept status = %d, %v; want success", acceptStatus, err)
	}

	if status, err := resp.readUint32(); err != nil || nfs4Status(status) != nfs4OK {
		t.Fatalf("compound status = %d, %v; want NFS4_OK", status, err)
	}
	tag, err := resp.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		t.Fatalf("failed to read response tag: %v", err)
	}
	if string(tag) != "root" {
		t.Fatalf("tag = %q, want root", tag)
	}
	resCount, err := resp.readUint32()
	if err != nil {
		t.Fatalf("failed to read result count: %v", err)
	}
	if resCount != 3 {
		t.Fatalf("result count = %d, want 3", resCount)
	}

	assertOpStatus(t, resp, opPutRootFH)

	assertOpStatus(t, resp, opGetFH)
	fh, err := resp.readOpaque(nfs4FhSize)
	if err != nil {
		t.Fatalf("failed to read GETFH handle: %v", err)
	}
	if len(fh) == 0 {
		t.Fatalf("GETFH returned empty handle")
	}

	assertOpStatus(t, resp, opGetAttr)
	mask, err := resp.readBitmap()
	if err != nil {
		t.Fatalf("failed to read GETATTR mask: %v", err)
	}
	if !bitmapHas(mask, fattr4Type) || !bitmapHas(mask, fattr4Mode) {
		t.Fatalf("GETATTR mask = %v, want type and mode", mask)
	}
	attrVals, err := resp.readOpaque(1024)
	if err != nil {
		t.Fatalf("failed to read GETATTR values: %v", err)
	}
	attrReader := newNFS4Reader(bytes.NewReader(attrVals))
	fileType, err := attrReader.readUint32()
	if err != nil {
		t.Fatalf("failed to read type attr: %v", err)
	}
	if fileType != uint32(FileTypeDirectory) {
		t.Fatalf("type attr = %d, want directory", fileType)
	}
}

func assertOpStatus(t *testing.T, resp *nfs4Reader, want nfs4Op) {
	t.Helper()
	op, err := resp.readUint32()
	if err != nil {
		t.Fatalf("failed to read op: %v", err)
	}
	if nfs4Op(op) != want {
		t.Fatalf("op = %d, want %d", op, want)
	}
	status, err := resp.readUint32()
	if err != nil {
		t.Fatalf("failed to read status for op %d: %v", want, err)
	}
	if nfs4Status(status) != nfs4OK {
		t.Fatalf("status for op %d = %d, want NFS4_OK", want, status)
	}
}

type nfs4TestHandler struct {
	fs      billy.Filesystem
	handles map[string][]string
}

func newNFSv4TestHandler(fs billy.Filesystem) *nfs4TestHandler {
	return &nfs4TestHandler{
		fs:      fs,
		handles: make(map[string][]string),
	}
}

func (h *nfs4TestHandler) Mount(context.Context, net.Conn, MountRequest) (MountStatus, billy.Filesystem, []AuthFlavor) {
	return MountStatusOk, h.fs, []AuthFlavor{AuthFlavorNull}
}

func (h *nfs4TestHandler) Change(fs billy.Filesystem) billy.Change {
	if c, ok := fs.(billy.Change); ok {
		return c
	}
	return nil
}

func (h *nfs4TestHandler) FSStat(context.Context, billy.Filesystem, *FSStat) error {
	return nil
}

func (h *nfs4TestHandler) ToHandle(_ billy.Filesystem, path []string) []byte {
	handle := []byte(fmt.Sprintf("fh-%d", len(h.handles)+1))
	cp := make([]string, len(path))
	copy(cp, path)
	h.handles[string(handle)] = cp
	return handle
}

func (h *nfs4TestHandler) FromHandle(handle []byte) (billy.Filesystem, []string, error) {
	path, ok := h.handles[string(handle)]
	if !ok {
		return nil, nil, fmt.Errorf("unknown handle")
	}
	cp := make([]string, len(path))
	copy(cp, path)
	return h.fs, cp, nil
}

func (h *nfs4TestHandler) InvalidateHandle(billy.Filesystem, []byte) error {
	return nil
}

func (h *nfs4TestHandler) HandleLimit() int {
	return 100
}
