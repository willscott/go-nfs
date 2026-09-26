package nfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/helpers/memfs"
)

// nfs4TestOp is one operation of a test COMPOUND: its number and arguments.
type nfs4TestOp struct {
	op   nfs4Op
	args interface{}
}

type nfs4RPCReplyHeader struct {
	Xid          uint32
	MsgType      uint32
	ReplyStat    uint32
	VerfFlavor   uint32
	VerfBody     []byte
	AcceptStatus uint32
}

func newNFS4TestServer(t *testing.T) (*Server, *nfs4TestHandler, billy.Filesystem) {
	t.Helper()
	fs := memfs.New()
	if err := fs.MkdirAll("/", 0755); err != nil {
		t.Fatalf("failed to create test root: %v", err)
	}
	handler := newNFSv4TestHandler(fs)
	srv := &Server{Handler: handler, ID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}
	return srv, handler, fs
}

// nfs4CompoundRequest encodes ops as the body of one COMPOUND call.
func nfs4CompoundRequest(t *testing.T, ops ...nfs4TestOp) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := xdr.Write(&body, nfs4CompoundArgs{Tag: []byte("test")}); err != nil {
		t.Fatal(err)
	}
	if err := xdr.Write(&body, uint32(len(ops))); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if err := xdr.Write(&body, op.op); err != nil {
			t.Fatal(err)
		}
		if op.args != nil {
			if err := xdr.Write(&body, op.args); err != nil {
				t.Fatalf("encoding args of op %d: %v", op.op, err)
			}
		}
	}
	return body.Bytes()
}

// nfs4CompoundReply runs the call in req through srv and returns the
// compound status and a reader positioned at the first result.
func nfs4CompoundReply(t *testing.T, srv *Server, handler Handler, req *request) (nfs4Status, io.Reader) {
	t.Helper()
	w := &response{
		conn:     &conn{Server: srv},
		req:      req,
		errorFmt: basicErrorFormatter,
		writer:   bytes.NewBuffer(nil),
	}
	if err := nfs4OnCompound(context.Background(), w, handler); err != nil {
		t.Fatalf("nfs4OnCompound returned error: %v", err)
	}

	resp := bytes.NewReader(w.writer.Bytes())
	var rpc nfs4RPCReplyHeader
	if err := xdr.Read(resp, &rpc); err != nil {
		t.Fatalf("failed to read RPC reply header: %v", err)
	}
	if rpc.Xid != req.xid || rpc.AcceptStatus != uint32(ResponseCodeSuccess) {
		t.Fatalf("RPC reply header = %+v, want xid %d and success", rpc, req.xid)
	}
	var header nfs4CompoundResHeader
	if err := xdr.Read(resp, &header); err != nil {
		t.Fatalf("failed to read COMPOUND header: %v", err)
	}
	if string(header.Tag) != "test" {
		t.Fatalf("tag = %q, want test", header.Tag)
	}
	return header.Status, resp
}

// nfs4RunCompound sends ops as one COMPOUND and returns the compound status
// and a reader positioned at the first result.
func nfs4RunCompound(t *testing.T, srv *Server, handler Handler, ops ...nfs4TestOp) (nfs4Status, io.Reader) {
	t.Helper()
	req := &request{xid: 1, Body: bytes.NewReader(nfs4CompoundRequest(t, ops...))}
	return nfs4CompoundReply(t, srv, handler, req)
}

// nfs4ExpectOp reads one result's header and then its body into res, if
// res is not nil.
func nfs4ExpectOp(t *testing.T, r io.Reader, op nfs4Op, status nfs4Status, res interface{}) {
	t.Helper()
	var header nfs4ResultHeader
	if err := xdr.Read(r, &header); err != nil {
		t.Fatalf("failed to read result header for op %d: %v", op, err)
	}
	if header.Op != op || header.Status != status {
		t.Fatalf("result = op %d status %d, want op %d status %d", header.Op, header.Status, op, status)
	}
	if res != nil {
		if err := xdr.Read(r, res); err != nil {
			t.Fatalf("failed to read result of op %d: %v", op, err)
		}
	}
}

// nfs4TestCompoundClient sets up a confirmed client ID for name with
// SETCLIENTID and SETCLIENTID_CONFIRM.
func nfs4TestCompoundClient(t *testing.T, srv *Server, handler Handler, name string) uint64 {
	t.Helper()
	status, resp := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpSetClientID, nfs4SetClientIDArgs{Verifier: [8]byte{1}, ID: []byte(name)}})
	if status != nfs4OK {
		t.Fatalf("SETCLIENTID: status = %d", status)
	}
	var res nfs4SetClientIDRes
	nfs4ExpectOp(t, resp, nfs4OpSetClientID, nfs4OK, &res)
	confirm := nfs4SetClientIDConfirmArgs(res)
	if status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpSetClientIDConfirm, confirm}); status != nfs4OK {
		t.Fatalf("SETCLIENTID_CONFIRM: status = %d", status)
	}
	return res.ClientID
}

func nfs4OpenFile(clientID uint64, name string, owner string, how *nfs4CreateHow) nfs4TestOp {
	args := nfs4OpenArgs{
		Seqid:       1,
		ShareAccess: nfs4ShareAccessRead | nfs4ShareAccessWrite,
		Owner:       nfs4Owner{ClientID: clientID, Owner: owner},
		Claim:       nfs4ClaimNull,
		File:        name,
	}
	if how != nil {
		args.OpenType = nfs4OpenCreate
		args.How = *how
	}
	return nfs4TestOp{nfs4OpOpen, args}
}

func TestNFSv4CompoundPutRootFHGetAttr(t *testing.T) {
	srv, handler, _ := newNFS4TestServer(t)

	status, resp := nfs4RunCompound(t, srv, handler,
		nfs4TestOp{nfs4OpPutRootFH, nil},
		nfs4TestOp{nfs4OpGetFH, nil},
		nfs4TestOp{nfs4OpGetAttr, nfs4GetAttrArgs{Request: nfs4BitmapOf(nfs4AttrType, nfs4AttrMode)}},
	)
	if status != nfs4OK {
		t.Fatalf("compound status = %d, want NFS4_OK", status)
	}

	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	var fh []byte
	nfs4ExpectOp(t, resp, nfs4OpGetFH, nfs4OK, &fh)
	if len(fh) == 0 {
		t.Fatalf("GETFH returned empty handle")
	}
	var attrs nfs4FAttr
	nfs4ExpectOp(t, resp, nfs4OpGetAttr, nfs4OK, &attrs)
	if !attrs.Mask.has(nfs4AttrType) || !attrs.Mask.has(nfs4AttrMode) {
		t.Fatalf("GETATTR mask = %v, want type and mode", attrs.Mask)
	}
	var vals struct {
		Type FileType
		Mode uint32
	}
	if err := xdr.Read(bytes.NewReader(attrs.Vals), &vals); err != nil {
		t.Fatalf("failed to read attribute values: %v", err)
	}
	if vals.Type != FileTypeDirectory {
		t.Fatalf("type attr = %d, want directory", vals.Type)
	}
}

// After a server restart a client reclaims its opens with CLAIM_PREVIOUS.
// This server keeps no state across a restart, so it must answer
// NFS4ERR_NO_GRACE, which sends the client to open the file afresh. Any
// other error makes a Linux client give up on the open file, failing its
// reads and writes with EIO.
func TestNFSv4OpenReclaimHasNoGrace(t *testing.T) {
	srv, handler, _ := newNFS4TestServer(t)
	client := nfs4TestCompoundClient(t, srv, handler, "c")

	reclaim := nfs4OpenFile(client, "f", "alice", nil)
	args := reclaim.args.(nfs4OpenArgs)
	args.Claim = nfs4ClaimPrevious
	args.File = ""
	reclaim.args = args

	status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, reclaim)
	if status != nfs4ErrNoGrace {
		t.Fatalf("OPEN with CLAIM_PREVIOUS: status = %d, want NFS4ERR_NO_GRACE (%d)", status, nfs4ErrNoGrace)
	}
}

func TestNFSv4OpenStateIDGenerations(t *testing.T) {
	srv, handler, _ := newNFS4TestServer(t)
	client := nfs4TestCompoundClient(t, srv, handler, "c")
	unchecked := &nfs4CreateHow{Mode: nfs4CreateUnchecked}

	open := func(name, owner string) nfs4OpenRes {
		t.Helper()
		status, resp := nfs4RunCompound(t, srv, handler,
			nfs4TestOp{nfs4OpPutRootFH, nil},
			nfs4OpenFile(client, name, owner, unchecked),
		)
		if status != nfs4OK {
			t.Fatalf("OPEN %s by %s: status = %d", name, owner, status)
		}
		nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
		var res nfs4OpenRes
		nfs4ExpectOp(t, resp, nfs4OpOpen, nfs4OK, &res)
		return res
	}

	first := open("f", "alice")
	if first.StateID.Seqid != 1 {
		t.Fatalf("first open seqid = %d, want 1", first.StateID.Seqid)
	}
	if first.RFlags&nfs4OpenResultLocktypePosix == 0 {
		t.Fatalf("rflags = %#x, want POSIX lock type", first.RFlags)
	}
	again := open("f", "alice")
	if again.StateID.Other != first.StateID.Other || again.StateID.Seqid != 2 {
		t.Fatalf("reopen stateid = %+v, want same state at seqid 2 (first %+v)", again.StateID, first.StateID)
	}
	if other := open("f", "bob"); other.StateID.Other == first.StateID.Other || other.StateID.Seqid != 1 {
		t.Fatalf("another owner's stateid = %+v, want a new state at seqid 1", other.StateID)
	}
	if other := open("g", "alice"); other.StateID.Other == first.StateID.Other {
		t.Fatal("another file must get its own stateid")
	}

	closeOp := nfs4TestOp{nfs4OpClose, nfs4CloseArgs{Seqid: 3, StateID: again.StateID}}
	status, resp := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, closeOp)
	if status != nfs4OK {
		t.Fatalf("CLOSE: status = %d", status)
	}
	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	var closed nfs4StateID
	nfs4ExpectOp(t, resp, nfs4OpClose, nfs4OK, &closed)
	if closed.Other != first.StateID.Other || closed.Seqid != 3 {
		t.Fatalf("CLOSE stateid = %+v, want seqid 3", closed)
	}

	status, _ = nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, closeOp)
	if status != nfs4ErrBadStateID {
		t.Fatalf("second CLOSE: status = %d, want BAD_STATEID", status)
	}
}

func TestNFSv4ExclusiveCreateActsGuarded(t *testing.T) {
	srv, handler, fs := newNFS4TestServer(t)
	client := nfs4TestCompoundClient(t, srv, handler, "c")
	exclusive := &nfs4CreateHow{Mode: nfs4CreateExclusive, Verifier: [8]byte{9}}

	status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4OpenFile(client, "new", "o", exclusive))
	if status != nfs4OK {
		t.Fatalf("exclusive create of a new file: status = %d", status)
	}
	if _, err := fs.Stat("new"); err != nil {
		t.Fatalf("exclusive create made no file: %v", err)
	}

	status, _ = nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4OpenFile(client, "new", "o", exclusive))
	if status != nfs4ErrExist {
		t.Fatalf("exclusive create of an existing file: status = %d, want EXIST", status)
	}
}

func TestNFSv4GuardedCreateAppliesAttributes(t *testing.T) {
	srv, handler, fs := newNFS4TestServer(t)
	client := nfs4TestCompoundClient(t, srv, handler, "c")
	var vals bytes.Buffer
	if err := xdr.Write(&vals, uint32(0640)); err != nil {
		t.Fatal(err)
	}
	guarded := &nfs4CreateHow{Mode: nfs4CreateGuarded, GuardedAttrs: nfs4FAttr{Mask: nfs4BitmapOf(nfs4AttrMode), Vals: vals.Bytes()}}

	status, resp := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4OpenFile(client, "f", "o", guarded))
	if status != nfs4OK {
		t.Fatalf("guarded create: status = %d", status)
	}
	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	var res nfs4OpenRes
	nfs4ExpectOp(t, resp, nfs4OpOpen, nfs4OK, &res)
	if !res.AttrSet.has(nfs4AttrMode) {
		t.Fatalf("attrset = %v, want mode", res.AttrSet)
	}
	info, err := fs.Stat("f")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestNFSv4SetAttrFailureCarriesAttrsSet(t *testing.T) {
	srv, handler, _ := newNFS4TestServer(t)
	// SETATTR of an attribute that cannot be set.
	args := nfs4SetAttrArgs{Attrs: nfs4FAttr{Mask: nfs4BitmapOf(nfs4AttrFileID)}}

	status, resp := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4TestOp{nfs4OpSetAttr, args})
	if status != nfs4ErrAttrNotSupp {
		t.Fatalf("status = %d, want ATTRNOTSUPP", status)
	}
	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	var set nfs4Bitmap
	nfs4ExpectOp(t, resp, nfs4OpSetAttr, nfs4ErrAttrNotSupp, &set)
	if len(set) != 0 {
		t.Fatalf("attrsset = %v, want empty", set)
	}
}

func TestNFSv4LockThroughCompound(t *testing.T) {
	srv, handler, _ := newNFS4TestServer(t)
	unchecked := &nfs4CreateHow{Mode: nfs4CreateUnchecked}
	lookup := nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "f"}}

	// lock opens f as the client and locks it under that open.
	lock := func(name string) (nfs4Owner, nfs4Status, io.Reader) {
		client := nfs4TestCompoundClient(t, srv, handler, name)
		status, resp := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4OpenFile(client, "f", "o", unchecked))
		if status != nfs4OK {
			t.Fatalf("OPEN: status = %d", status)
		}
		nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
		var opened nfs4OpenRes
		nfs4ExpectOp(t, resp, nfs4OpOpen, nfs4OK, &opened)

		owner := nfs4Owner{ClientID: client, Owner: name}
		status, resp = nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, lookup, nfs4TestOp{nfs4OpLock, nfs4LockArgs{
			LockType: nfs4WriteLT,
			Offset:   0,
			Length:   100,
			Locker: nfs4Locker{
				NewLockOwner: true,
				OpenOwner: nfs4OpenToLockOwner{
					OpenStateID: opened.StateID,
					LockOwner:   owner,
				},
			},
		}})
		nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
		nfs4ExpectOp(t, resp, nfs4OpLookup, nfs4OK, nil)
		return owner, status, resp
	}

	alice, status, resp := lock("alice")
	if status != nfs4OK {
		t.Fatalf("LOCK: status = %d", status)
	}
	var locked nfs4StateID
	nfs4ExpectOp(t, resp, nfs4OpLock, nfs4OK, &locked)
	if locked.Seqid != 1 {
		t.Fatalf("lock stateid = %+v, want a new state at seqid 1", locked)
	}

	_, status, resp = lock("bob")
	if status != nfs4ErrDenied {
		t.Fatalf("conflicting LOCK: status = %d, want DENIED", status)
	}
	var denied nfs4LockDenied
	nfs4ExpectOp(t, resp, nfs4OpLock, nfs4ErrDenied, &denied)
	want := nfs4LockDenied{Offset: 0, Length: 100, LockType: nfs4WriteLT, Owner: alice}
	if denied != want {
		t.Fatalf("denied = %+v, want %+v", denied, want)
	}
}

type nfs4TestHandler struct {
	fs      billy.Filesystem
	handles map[string][]string
	next    int
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

// ToHandle returns a path's existing handle, as CachingHandler does, so
// operations that invalidate a path's handle reach the one clients hold.
func (h *nfs4TestHandler) ToHandle(_ billy.Filesystem, path []string) []byte {
	for handle, p := range h.handles {
		if strings.Join(p, "/") == strings.Join(path, "/") {
			return []byte(handle)
		}
	}
	h.next++
	handle := []byte(fmt.Sprintf("fh-%d", h.next))
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

func (h *nfs4TestHandler) InvalidateHandle(_ billy.Filesystem, handle []byte) error {
	delete(h.handles, string(handle))
	return nil
}

func (h *nfs4TestHandler) HandleLimit() int {
	return 100
}

func TestNFSv4SupportedAttrsIncludeSettableTimes(t *testing.T) {
	// Linux masks SETATTR by supported_attrs, so without these it never
	// sends times and touch -d silently does nothing.
	for _, attr := range []nfs4Attr{nfs4AttrTimeAccessSet, nfs4AttrTimeModifySet} {
		if !nfs4SupportedAttrs.has(attr) {
			t.Errorf("supported_attrs lacks settable attribute %d", attr)
		}
	}
	for _, attr := range nfs4WritableAttrs.attrs() {
		if !nfs4SupportedAttrs.has(attr) {
			t.Errorf("writable attribute %d is not in supported_attrs", attr)
		}
	}
}

// A client keeps using a file's handle after renaming it. The handle names
// the old path, so RENAME must make it stale, sending the client to look the
// new name up, rather than leave it answering NFS4ERR_NOENT.
func TestNFSv4RenameInvalidatesOldHandle(t *testing.T) {
	srv, handler, fs := newNFS4TestServer(t)
	f, err := fs.Create("old")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	status, resp := nfs4RunCompound(t, srv, handler,
		nfs4TestOp{nfs4OpPutRootFH, nil},
		nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "old"}},
		nfs4TestOp{nfs4OpGetFH, nil},
	)
	if status != nfs4OK {
		t.Fatalf("LOOKUP: status = %d", status)
	}
	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	nfs4ExpectOp(t, resp, nfs4OpLookup, nfs4OK, nil)
	var fh []byte
	nfs4ExpectOp(t, resp, nfs4OpGetFH, nfs4OK, &fh)

	status, _ = nfs4RunCompound(t, srv, handler,
		nfs4TestOp{nfs4OpPutRootFH, nil},
		nfs4TestOp{nfs4OpSaveFH, nil},
		nfs4TestOp{nfs4OpRename, nfs4RenameArgs{OldName: "old", NewName: "new"}},
	)
	if status != nfs4OK {
		t.Fatalf("RENAME: status = %d", status)
	}

	getattr := nfs4TestOp{nfs4OpGetAttr, nfs4GetAttrArgs{Request: nfs4BitmapOf(nfs4AttrSize)}}
	if status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutFH, nfs4PutFHArgs{Handle: fh}}, getattr); status != nfs4ErrStale {
		t.Fatalf("GETATTR through the old handle: status = %d, want STALE", status)
	}
	if status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "new"}}, getattr); status != nfs4OK {
		t.Fatalf("GETATTR of the new name: status = %d", status)
	}
}

// nfs4ReadOnlyFS hides billy's write capability, as a read-only export does.
type nfs4ReadOnlyFS struct{ billy.Filesystem }

func (nfs4ReadOnlyFS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}

func TestNFSv4ReadOnlyFilesystemRefusesChanges(t *testing.T) {
	srv, _, fs := newNFS4TestServer(t)
	handler := newNFSv4TestHandler(nfs4ReadOnlyFS{fs})
	client := nfs4TestCompoundClient(t, srv, handler, "c")
	if err := fs.MkdirAll("/d", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Create("/f")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	root := nfs4TestOp{nfs4OpPutRootFH, nil}
	lookup := nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "f"}}
	readOpen := nfs4OpenFile(client, "f", "o", nil)
	readArgs := readOpen.args.(nfs4OpenArgs)
	readArgs.ShareAccess = nfs4ShareAccessRead
	readOpen.args = readArgs
	cases := []struct {
		name string
		ops  []nfs4TestOp
	}{
		{"WRITE", []nfs4TestOp{root, lookup, {nfs4OpWrite, nfs4WriteArgs{Data: []byte("x")}}}},
		{"SETATTR", []nfs4TestOp{root, lookup, {nfs4OpSetAttr, nfs4SetAttrArgs{Attrs: nfs4FAttr{Mask: nfs4BitmapOf(nfs4AttrSize), Vals: make([]byte, 8)}}}}},
		{"REMOVE", []nfs4TestOp{root, {nfs4OpRemove, nfs4RemoveArgs{Name: "f"}}}},
		{"RENAME", []nfs4TestOp{root, {nfs4OpSaveFH, nil}, {nfs4OpRename, nfs4RenameArgs{OldName: "f", NewName: "g"}}}},
		{"CREATE", []nfs4TestOp{root, {nfs4OpCreate, nfs4CreateArgs{Type: FileTypeDirectory, Name: "e"}}}},
		{"OPEN create", []nfs4TestOp{root, nfs4OpenFile(client, "new", "o", &nfs4CreateHow{Mode: nfs4CreateUnchecked})}},
		{"OPEN for write", []nfs4TestOp{root, nfs4OpenFile(client, "f", "o", nil)}},
	}
	for _, tc := range cases {
		if status, _ := nfs4RunCompound(t, srv, handler, tc.ops...); status != nfs4ErrROFS {
			t.Errorf("%s: status = %d, want ROFS", tc.name, status)
		}
	}
	if info, err := fs.Stat("/f"); err != nil || info.Size() != 0 {
		t.Fatalf("file changed on a read-only filesystem: %v, %v", info, err)
	}
	if _, err := fs.Stat("/new"); err == nil {
		t.Fatal("OPEN created a file on a read-only filesystem")
	}

	if status, _ := nfs4RunCompound(t, srv, handler, root, readOpen); status != nfs4OK {
		t.Fatalf("OPEN for read: status = %d", status)
	}
	status, resp := nfs4RunCompound(t, srv, handler, root, lookup, nfs4TestOp{nfs4OpAccess, nfs4AccessArgs{Access: 0x3f}})
	if status != nfs4OK {
		t.Fatalf("ACCESS: status = %d", status)
	}
	nfs4ExpectOp(t, resp, nfs4OpPutRootFH, nfs4OK, nil)
	nfs4ExpectOp(t, resp, nfs4OpLookup, nfs4OK, nil)
	var access nfs4AccessRes
	nfs4ExpectOp(t, resp, nfs4OpAccess, nfs4OK, &access)
	if want := nfs4AccessRead | nfs4AccessLookup | nfs4AccessExecute; access.Access != want {
		t.Fatalf("ACCESS granted %#x, want %#x", access.Access, want)
	}
}

func TestNFSv4StateFromAnotherServerIsStale(t *testing.T) {
	srv, handler, fs := newNFS4TestServer(t)
	f, err := fs.Create("/f")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpRenew, nfs4RenewArgs{ClientID: 0x1234}}); status != nfs4ErrStaleClientID {
		t.Fatalf("RENEW of an unknown client: status = %d, want STALE_CLIENTID", status)
	}
	stale := nfs4StateID{Seqid: 1, Other: [nfs4OtherSize]byte{1, 2, 3, 4, 0, 0, 0, 0, 0, 0, 0, 1}}
	if stale.Other[0] == byte(srv.nfs4State().boot>>24) {
		stale.Other[0]++
	}
	write := nfs4TestOp{nfs4OpWrite, nfs4WriteArgs{StateID: stale, Data: []byte("x")}}
	if status, _ := nfs4RunCompound(t, srv, handler, nfs4TestOp{nfs4OpPutRootFH, nil}, nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "f"}}, write); status != nfs4ErrStaleStateID {
		t.Fatalf("WRITE with another server's stateid: status = %d, want STALE_STATEID", status)
	}
	if info, _ := fs.Stat("/f"); info.Size() != 0 {
		t.Fatal("WRITE with a stale stateid changed the file")
	}
}

func TestNFSv4DotNamesAreBad(t *testing.T) {
	srv, handler, fs := newNFS4TestServer(t)
	client := nfs4TestCompoundClient(t, srv, handler, "c")
	if err := fs.MkdirAll("/a/b", 0755); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/c", 0755); err != nil {
		t.Fatal(err)
	}

	root := nfs4TestOp{nfs4OpPutRootFH, nil}
	inB := []nfs4TestOp{root, {nfs4OpLookup, nfs4LookupArgs{Name: "a"}}, {nfs4OpLookup, nfs4LookupArgs{Name: "b"}}}
	withB := func(ops ...nfs4TestOp) []nfs4TestOp {
		return append(append([]nfs4TestOp{}, inB...), ops...)
	}
	for _, name := range []string{".", ".."} {
		cases := map[string][]nfs4TestOp{
			"LOOKUP": withB(nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: name}}),
			"REMOVE": withB(nfs4TestOp{nfs4OpRemove, nfs4RemoveArgs{Name: name}}),
			"CREATE": withB(nfs4TestOp{nfs4OpCreate, nfs4CreateArgs{Type: FileTypeDirectory, Name: name}}),
			"OPEN":   withB(nfs4OpenFile(client, name, "o", &nfs4CreateHow{Mode: nfs4CreateUnchecked})),
			"RENAME from": withB(nfs4TestOp{nfs4OpSaveFH, nil}, root,
				nfs4TestOp{nfs4OpLookup, nfs4LookupArgs{Name: "c"}},
				nfs4TestOp{nfs4OpRename, nfs4RenameArgs{OldName: name, NewName: "x"}}),
			"RENAME to": withB(nfs4TestOp{nfs4OpSaveFH, nil},
				nfs4TestOp{nfs4OpRename, nfs4RenameArgs{OldName: "b", NewName: name}}),
		}
		for op, ops := range cases {
			if status, _ := nfs4RunCompound(t, srv, handler, ops...); status != nfs4ErrBadName {
				t.Errorf("%s %q: status = %d, want BADNAME", op, name, status)
			}
		}
	}
	if _, err := fs.Stat("/a/b"); err != nil {
		t.Fatalf("/a/b is gone: %v", err)
	}
	if _, err := fs.Stat("/c/x"); err == nil {
		t.Fatal("RENAME moved a directory named by \"..\"")
	}
}
