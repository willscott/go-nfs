package nfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/go-git/go-billy/v5"
	xdr2 "github.com/rasky/go-xdr/xdr2"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// NFSv4.0 (RFC 7530). A COMPOUND request carries a list of operations. Each
// operation has its handler in nfs4_on<op>.go, as each NFSv3 procedure has
// one in nfs_on<proc>.go.

const (
	nfs4Version       = 4
	nfs4ProcNull      = 0
	nfs4ProcCompound  = 1
	nfs4FhSize        = 128
	nfs4OtherSize     = 12
	nfs4OpaqueLimit   = 1024
	nfs4MaxRead       = MaxRead
	nfs4MaxWrite      = MaxRead
	nfs4LeaseTimeSecs = 90

	// nfs4ArgLimit bounds any variable-length item in operation arguments
	// other than WRITE data, so a malformed length cannot make the decoder
	// allocate much memory.
	nfs4ArgLimit = 64 << 10
)

type nfs4Status uint32

const (
	nfs4OK                   nfs4Status = 0
	nfs4ErrPerm              nfs4Status = 1
	nfs4ErrNoEnt             nfs4Status = 2
	nfs4ErrIO                nfs4Status = 5
	nfs4ErrAccess            nfs4Status = 13
	nfs4ErrExist             nfs4Status = 17
	nfs4ErrNotDir            nfs4Status = 20
	nfs4ErrIsDir             nfs4Status = 21
	nfs4ErrInval             nfs4Status = 22
	nfs4ErrFBig              nfs4Status = 27
	nfs4ErrNoSpc             nfs4Status = 28
	nfs4ErrNotEmpty          nfs4Status = 66
	nfs4ErrStale             nfs4Status = 70
	nfs4ErrBadHandle         nfs4Status = 10001
	nfs4ErrBadCookie         nfs4Status = 10003
	nfs4ErrNotSupp           nfs4Status = 10004
	nfs4ErrTooSmall          nfs4Status = 10005
	nfs4ErrServerFault       nfs4Status = 10006
	nfs4ErrBadType           nfs4Status = 10007
	nfs4ErrDenied            nfs4Status = 10010
	nfs4ErrResource          nfs4Status = 10018
	nfs4ErrNoFileHandle      nfs4Status = 10020
	nfs4ErrMinorVersMismatch nfs4Status = 10021
	nfs4ErrBadStateID        nfs4Status = 10025
	nfs4ErrAttrNotSupp       nfs4Status = 10032
	nfs4ErrNoGrace           nfs4Status = 10033
	nfs4ErrBadXDR            nfs4Status = 10036
	nfs4ErrBadName           nfs4Status = 10041
	nfs4ErrOpIllegal         nfs4Status = 10044
)

// nfs4Op is an NFSv4 operation number (nfs_opnum4).
type nfs4Op uint32

const (
	nfs4OpAccess             nfs4Op = 3
	nfs4OpClose              nfs4Op = 4
	nfs4OpCommit             nfs4Op = 5
	nfs4OpCreate             nfs4Op = 6
	nfs4OpGetAttr            nfs4Op = 9
	nfs4OpGetFH              nfs4Op = 10
	nfs4OpLink               nfs4Op = 11
	nfs4OpLock               nfs4Op = 12
	nfs4OpLockT              nfs4Op = 13
	nfs4OpLockU              nfs4Op = 14
	nfs4OpLookup             nfs4Op = 15
	nfs4OpLookupP            nfs4Op = 16
	nfs4OpOpen               nfs4Op = 18
	nfs4OpOpenConfirm        nfs4Op = 20
	nfs4OpOpenDowngrade      nfs4Op = 21
	nfs4OpPutFH              nfs4Op = 22
	nfs4OpPutPubFH           nfs4Op = 23
	nfs4OpPutRootFH          nfs4Op = 24
	nfs4OpRead               nfs4Op = 25
	nfs4OpReadDir            nfs4Op = 26
	nfs4OpReadLink           nfs4Op = 27
	nfs4OpRemove             nfs4Op = 28
	nfs4OpRename             nfs4Op = 29
	nfs4OpRenew              nfs4Op = 30
	nfs4OpRestoreFH          nfs4Op = 31
	nfs4OpSaveFH             nfs4Op = 32
	nfs4OpSecInfo            nfs4Op = 33
	nfs4OpSetAttr            nfs4Op = 34
	nfs4OpSetClientID        nfs4Op = 35
	nfs4OpSetClientIDConfirm nfs4Op = 36
	nfs4OpWrite              nfs4Op = 38
	nfs4OpReleaseLockOwner   nfs4Op = 39
	nfs4OpIllegal            nfs4Op = 10044
)

// nfs4OpHandler decodes one operation's arguments from args and encodes its
// result, the part of nfs_resop4 after the status, to res. It writes res
// only for a result that has a body: every success, and those failures whose
// result carries one (SETATTR's attrsset, LOCK and LOCKT's LOCK4denied).
type nfs4OpHandler func(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status

var nfs4OpHandlers = map[nfs4Op]nfs4OpHandler{
	nfs4OpAccess:             nfs4OnAccess,
	nfs4OpClose:              nfs4OnClose,
	nfs4OpCommit:             nfs4OnCommit,
	nfs4OpCreate:             nfs4OnCreate,
	nfs4OpGetAttr:            nfs4OnGetAttr,
	nfs4OpGetFH:              nfs4OnGetFH,
	nfs4OpLink:               nfs4OnNotSupp,
	nfs4OpLock:               nfs4OnLock,
	nfs4OpLockT:              nfs4OnLockT,
	nfs4OpLockU:              nfs4OnLockU,
	nfs4OpLookup:             nfs4OnLookup,
	nfs4OpLookupP:            nfs4OnLookupP,
	nfs4OpOpen:               nfs4OnOpen,
	nfs4OpOpenConfirm:        nfs4OnOpenConfirm,
	nfs4OpOpenDowngrade:      nfs4OnOpenDowngrade,
	nfs4OpPutFH:              nfs4OnPutFH,
	nfs4OpPutPubFH:           nfs4OnPutRootFH,
	nfs4OpPutRootFH:          nfs4OnPutRootFH,
	nfs4OpRead:               nfs4OnRead,
	nfs4OpReadDir:            nfs4OnReadDir,
	nfs4OpReadLink:           nfs4OnReadLink,
	nfs4OpRemove:             nfs4OnRemove,
	nfs4OpRename:             nfs4OnRename,
	nfs4OpRenew:              nfs4OnRenew,
	nfs4OpRestoreFH:          nfs4OnRestoreFH,
	nfs4OpSaveFH:             nfs4OnSaveFH,
	nfs4OpSecInfo:            nfs4OnSecInfo,
	nfs4OpSetAttr:            nfs4OnSetAttr,
	nfs4OpSetClientID:        nfs4OnSetClientID,
	nfs4OpSetClientIDConfirm: nfs4OnSetClientIDConfirm,
	nfs4OpWrite:              nfs4OnWrite,
	nfs4OpReleaseLockOwner:   nfs4OnReleaseLockOwner,
}

func init() {
	_ = RegisterVersionedMessageHandler(nfsServiceID, nfs4Version, nfs4ProcNull, onNull)
	_ = RegisterVersionedMessageHandler(nfsServiceID, nfs4Version, nfs4ProcCompound, nfs4OnCompound)
}

// nfs4Compound is what one COMPOUND request carries from operation to
// operation: the call and the current and saved filehandles.
type nfs4Compound struct {
	ctx     context.Context
	w       *response
	handler Handler
	current *nfs4FileHandle
	saved   *nfs4FileHandle
}

type nfs4FileHandle struct {
	handle []byte
	fs     billy.Filesystem
	path   []string
}

// nfs4StateID is a stateid4: Seqid is the state's generation, Other names it.
type nfs4StateID struct {
	Seqid uint32
	Other [nfs4OtherSize]byte
}

// nfs4Owner is an open_owner4 or lock_owner4: the client's short-hand id and
// the client-chosen owner bytes.
type nfs4Owner struct {
	ClientID uint64
	Owner    string
}

type nfs4ChangeInfo struct {
	Atomic bool
	Before uint64
	After  uint64
}

type nfs4CompoundArgs struct {
	Tag          []byte
	MinorVersion uint32
}

type nfs4CompoundResHeader struct {
	Status     nfs4Status
	Tag        []byte
	NumResults uint32
}

type nfs4ResultHeader struct {
	Op     nfs4Op
	Status nfs4Status
}

type nfs4Result struct {
	op     nfs4Op
	status nfs4Status
	body   []byte
}

func nfs4OnCompound(ctx context.Context, w *response, userHandle Handler) error {
	var args nfs4CompoundArgs
	if err := xdrReadLimited(w.req.Body, &args, nfs4OpaqueLimit); err != nil {
		return nfs4WriteCompound(w, nfs4ErrBadXDR, nil, nil)
	}
	opCount, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return nfs4WriteCompound(w, nfs4ErrBadXDR, args.Tag, nil)
	}
	if args.MinorVersion != 0 {
		return nfs4WriteCompound(w, nfs4ErrMinorVersMismatch, args.Tag, nil)
	}

	c := &nfs4Compound{ctx: ctx, w: w, handler: userHandle}
	var results []nfs4Result
	status := nfs4OK
	for i := uint32(0); i < opCount && status == nfs4OK; i++ {
		opNum, err := xdr.ReadUint32(w.req.Body)
		if err != nil {
			results = append(results, nfs4Result{op: nfs4OpIllegal, status: nfs4ErrBadXDR})
			status = nfs4ErrBadXDR
			break
		}
		op := nfs4Op(opNum)
		handler, ok := nfs4OpHandlers[op]
		if !ok {
			op, handler = nfs4OpIllegal, nfs4OnIllegal
		}
		var body bytes.Buffer
		status = handler(c, w.req.Body, &body)
		if status != nfs4OK {
			Log.Debugf("nfs4 op %d returned status %d", op, status)
		}
		results = append(results, nfs4Result{op: op, status: status, body: body.Bytes()})
	}
	return nfs4WriteCompound(w, status, args.Tag, results)
}

func nfs4WriteCompound(w *response, status nfs4Status, tag []byte, results []nfs4Result) error {
	var out bytes.Buffer
	header := nfs4CompoundResHeader{Status: status, Tag: tag, NumResults: uint32(len(results))}
	if err := xdr.Write(&out, header); err != nil {
		return err
	}
	for _, res := range results {
		if err := xdr.Write(&out, nfs4ResultHeader{Op: res.op, Status: res.status}); err != nil {
			return err
		}
		out.Write(res.body)
	}
	return w.Write(out.Bytes())
}

func nfs4OnNotSupp(*nfs4Compound, io.Reader, io.Writer) nfs4Status {
	return nfs4ErrNotSupp
}

func nfs4OnIllegal(*nfs4Compound, io.Reader, io.Writer) nfs4Status {
	return nfs4ErrOpIllegal
}

// xdrReadLimited decodes v like xdr.Read, but refuses any variable-length item
// longer than limit instead of allocating whatever length a request claims.
func xdrReadLimited(r io.Reader, v interface{}, limit uint) error {
	_, err := xdr2.UnmarshalLimited(r, v, limit)
	return err
}

// nfs4Decode reads an operation's arguments.
func nfs4Decode(args io.Reader, v interface{}) nfs4Status {
	if err := xdrReadLimited(args, v, nfs4ArgLimit); err != nil {
		return nfs4ErrBadXDR
	}
	return nfs4OK
}

// nfs4Encode writes an operation's result.
func nfs4Encode(res io.Writer, v interface{}) nfs4Status {
	if err := xdr.Write(res, v); err != nil {
		return nfs4ErrServerFault
	}
	return nfs4OK
}

func (c *nfs4Compound) requireCurrent() (*nfs4FileHandle, nfs4Status) {
	if c.current == nil {
		return nil, nfs4ErrNoFileHandle
	}
	return c.current, nfs4OK
}

// setCurrent makes the file at p on fs the current filehandle.
func (c *nfs4Compound) setCurrent(fs billy.Filesystem, p []string) nfs4Status {
	handle := c.handler.ToHandle(fs, p)
	if len(handle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	c.current = &nfs4FileHandle{handle: handle, fs: fs, path: p}
	return nfs4OK
}

func (fh *nfs4FileHandle) clone() *nfs4FileHandle {
	if fh == nil {
		return nil
	}
	return &nfs4FileHandle{
		handle: append([]byte(nil), fh.handle...),
		fs:     fh.fs,
		path:   nfs4CopyPath(fh.path),
	}
}

// fullPath is the file's path on its filesystem.
func (fh *nfs4FileHandle) fullPath() string {
	return nfs4Join(fh.fs, fh.path)
}

// child is the path of name in the directory fh.
func (fh *nfs4FileHandle) child(name string) []string {
	return nfs4AppendPath(fh.path, name)
}

func (fh *nfs4FileHandle) ensureDir() nfs4Status {
	info, err := fh.fs.Lstat(fh.fullPath())
	if err != nil {
		return nfs4StatusFromErr(err)
	}
	if !info.IsDir() {
		return nfs4ErrNotDir
	}
	return nfs4OK
}

// changeID stands in for the change attribute, which billy filesystems do
// not keep: it moves whenever the file's size or modification time does.
func (fh *nfs4FileHandle) changeID() uint64 {
	return nfs4ChangeID(fh.fs, fh.path)
}

// nfs4Component checks a component4: a single, non-empty path element.
func nfs4Component(name string) nfs4Status {
	if name == "" || len(name) > nfs4OpaqueLimit || strings.ContainsAny(name, "/\x00") {
		return nfs4ErrBadName
	}
	return nfs4OK
}

func nfs4Join(fs billy.Filesystem, parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return fs.Join(parts...)
}

func nfs4AppendPath(base []string, elem string) []string {
	out := nfs4CopyPath(base)
	if elem != "" {
		out = append(out, elem)
	}
	return out
}

func nfs4CopyPath(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func nfs4StatusFromErr(err error) nfs4Status {
	if err == nil {
		return nfs4OK
	}
	var nfsErr *NFSStatusError
	if errors.As(err, &nfsErr) {
		return nfs4StatusFromNFS3(nfsErr.NFSStatus)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nfs4ErrNoEnt
	}
	if errors.Is(err, os.ErrPermission) {
		return nfs4ErrAccess
	}
	if errors.Is(err, os.ErrExist) {
		return nfs4ErrExist
	}
	if errors.Is(err, syscall.ENOSPC) {
		return nfs4ErrNoSpc
	}
	if errors.Is(err, io.ErrShortBuffer) {
		return nfs4ErrTooSmall
	}
	return nfs4ErrIO
}

func nfs4StatusFromNFS3(status NFSStatus) nfs4Status {
	switch status {
	case NFSStatusOk:
		return nfs4OK
	case NFSStatusPerm:
		return nfs4ErrPerm
	case NFSStatusNoEnt:
		return nfs4ErrNoEnt
	case NFSStatusIO:
		return nfs4ErrIO
	case NFSStatusAccess:
		return nfs4ErrAccess
	case NFSStatusExist:
		return nfs4ErrExist
	case NFSStatusNotDir:
		return nfs4ErrNotDir
	case NFSStatusIsDir:
		return nfs4ErrIsDir
	case NFSStatusInval:
		return nfs4ErrInval
	case NFSStatusFBig:
		return nfs4ErrFBig
	case NFSStatusNoSPC:
		return nfs4ErrNoSpc
	case NFSStatusNotEmpty:
		return nfs4ErrNotEmpty
	case NFSStatusStale:
		return nfs4ErrStale
	case NFSStatusBadHandle:
		return nfs4ErrBadHandle
	case NFSStatusBadCookie:
		return nfs4ErrBadCookie
	case NFSStatusNotSupp:
		return nfs4ErrNotSupp
	case NFSStatusTooSmall:
		return nfs4ErrTooSmall
	case NFSStatusServerFault:
		return nfs4ErrServerFault
	case NFSStatusBadType:
		return nfs4ErrBadType
	default:
		return nfs4ErrIO
	}
}
