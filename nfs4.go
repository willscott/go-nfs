package nfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/go-git/go-billy/v5"
)

const (
	nfs4Version       = 4
	nfs4ProcNull      = 0
	nfs4ProcCompound  = 1
	nfs4FhSize        = 128
	nfs4VerifierSize  = 8
	nfs4OtherSize     = 12
	nfs4OpaqueLimit   = 1024
	nfs4MaxRead       = MaxRead
	nfs4MaxWrite      = MaxRead
	nfs4LeaseTimeSecs = 90
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
	nfs4ErrOpenMode          nfs4Status = 10038
	nfs4ErrBadName           nfs4Status = 10041
	nfs4ErrOpIllegal         nfs4Status = 10044
)

type nfs4Op uint32

const (
	opAccess             nfs4Op = 3
	opClose              nfs4Op = 4
	opCommit             nfs4Op = 5
	opCreate             nfs4Op = 6
	opGetAttr            nfs4Op = 9
	opGetFH              nfs4Op = 10
	opLink               nfs4Op = 11
	opLock               nfs4Op = 12
	opLockT              nfs4Op = 13
	opLockU              nfs4Op = 14
	opLookup             nfs4Op = 15
	opLookupP            nfs4Op = 16
	opOpen               nfs4Op = 18
	opOpenConfirm        nfs4Op = 20
	opOpenDowngrade      nfs4Op = 21
	opPutFH              nfs4Op = 22
	opPutPubFH           nfs4Op = 23
	opPutRootFH          nfs4Op = 24
	opRead               nfs4Op = 25
	opReadDir            nfs4Op = 26
	opReadLink           nfs4Op = 27
	opRemove             nfs4Op = 28
	opRename             nfs4Op = 29
	opRenew              nfs4Op = 30
	opRestoreFH          nfs4Op = 31
	opSaveFH             nfs4Op = 32
	opSecInfo            nfs4Op = 33
	opSetAttr            nfs4Op = 34
	opSetClientID        nfs4Op = 35
	opSetClientIDConfirm nfs4Op = 36
	opWrite              nfs4Op = 38
	opReleaseLockOwner   nfs4Op = 39
	opIllegal            nfs4Op = 10044
)

const (
	fattr4SupportedAttrs  uint32 = 0
	fattr4Type            uint32 = 1
	fattr4FHExpireType    uint32 = 2
	fattr4Change          uint32 = 3
	fattr4Size            uint32 = 4
	fattr4LinkSupport     uint32 = 5
	fattr4SymlinkSupport  uint32 = 6
	fattr4NamedAttr       uint32 = 7
	fattr4FSID            uint32 = 8
	fattr4UniqueHandles   uint32 = 9
	fattr4LeaseTime       uint32 = 10
	fattr4RDAttrError     uint32 = 11
	fattr4ACLSupport      uint32 = 13
	fattr4CanSetTime      uint32 = 15
	fattr4CaseInsensitive uint32 = 16
	fattr4CasePreserving  uint32 = 17
	fattr4ChownRestricted uint32 = 18
	fattr4FileHandle      uint32 = 19
	fattr4FileID          uint32 = 20
	fattr4FilesAvail      uint32 = 21
	fattr4FilesFree       uint32 = 22
	fattr4FilesTotal      uint32 = 23
	fattr4Hidden          uint32 = 25
	fattr4Homogeneous     uint32 = 26
	fattr4MaxFileSize     uint32 = 27
	fattr4MaxLink         uint32 = 28
	fattr4MaxName         uint32 = 29
	fattr4MaxRead         uint32 = 30
	fattr4MaxWrite        uint32 = 31
	fattr4Mode            uint32 = 33
	fattr4NoTrunc         uint32 = 34
	fattr4NumLinks        uint32 = 35
	fattr4Owner           uint32 = 36
	fattr4OwnerGroup      uint32 = 37
	fattr4RawDev          uint32 = 41
	fattr4SpaceAvail      uint32 = 42
	fattr4SpaceFree       uint32 = 43
	fattr4SpaceTotal      uint32 = 44
	fattr4SpaceUsed       uint32 = 45
	fattr4System          uint32 = 46
	fattr4TimeAccess      uint32 = 47
	fattr4TimeAccessSet   uint32 = 48
	fattr4TimeDelta       uint32 = 51
	fattr4TimeMetadata    uint32 = 52
	fattr4TimeModify      uint32 = 53
	fattr4TimeModifySet   uint32 = 54
	fattr4MountedOnFileID uint32 = 55
)

const (
	access4Read    uint32 = 0x00000001
	access4Lookup  uint32 = 0x00000002
	access4Modify  uint32 = 0x00000004
	access4Extend  uint32 = 0x00000008
	access4Delete  uint32 = 0x00000010
	access4Execute uint32 = 0x00000020

	open4NoCreate         uint32 = 0
	open4Create           uint32 = 1
	open4Unchecked        uint32 = 0
	open4Guarded          uint32 = 1
	open4Exclusive        uint32 = 2
	open4ShareAccessRead  uint32 = 0x00000001
	open4ShareAccessWrite uint32 = 0x00000002
	open4ShareDenyNone    uint32 = 0
	claimNull             uint32 = 0

	nf4Reg  uint32 = 1
	nf4Dir  uint32 = 2
	nf4Blk  uint32 = 3
	nf4Chr  uint32 = 4
	nf4Lnk  uint32 = 5
	nf4Sock uint32 = 6
	nf4FIFO uint32 = 7

	openDelegateNone uint32 = 0

	// open4ResultLocktypePosix in OPEN rflags advertises POSIX byte-range
	// lock semantics; without it the Linux client fails fcntl locks locally
	// with ENOLCK and never sends LOCK. (0x2 is OPEN4_RESULT_CONFIRM.)
	open4ResultLocktypePosix uint32 = 0x4

	authFlavorNull uint32 = 0
	authFlavorUnix uint32 = 1
)

var (
	nfs4SupportedAttrIDs = []uint32{
		fattr4SupportedAttrs,
		fattr4Type,
		fattr4FHExpireType,
		fattr4Change,
		fattr4Size,
		fattr4LinkSupport,
		fattr4SymlinkSupport,
		fattr4NamedAttr,
		fattr4FSID,
		fattr4UniqueHandles,
		fattr4LeaseTime,
		fattr4RDAttrError,
		fattr4ACLSupport,
		fattr4CanSetTime,
		fattr4CaseInsensitive,
		fattr4CasePreserving,
		fattr4ChownRestricted,
		fattr4FileHandle,
		fattr4FileID,
		fattr4FilesAvail,
		fattr4FilesFree,
		fattr4FilesTotal,
		fattr4Hidden,
		fattr4Homogeneous,
		fattr4MaxFileSize,
		fattr4MaxLink,
		fattr4MaxName,
		fattr4MaxRead,
		fattr4MaxWrite,
		fattr4Mode,
		fattr4NoTrunc,
		fattr4NumLinks,
		fattr4Owner,
		fattr4OwnerGroup,
		fattr4RawDev,
		fattr4SpaceAvail,
		fattr4SpaceFree,
		fattr4SpaceTotal,
		fattr4SpaceUsed,
		fattr4System,
		fattr4TimeAccess,
		fattr4TimeDelta,
		fattr4TimeMetadata,
		fattr4TimeModify,
		fattr4MountedOnFileID,
	}
	nfs4SupportedAttrBitmap = bitmapFromAttrs(nfs4SupportedAttrIDs...)
	nfs4WriteAttrIDs        = []uint32{fattr4Size, fattr4Mode, fattr4TimeAccessSet, fattr4TimeModifySet}
)

func init() {
	_ = RegisterVersionedMessageHandler(nfsServiceID, nfs4Version, nfs4ProcNull, onNull)
	_ = RegisterVersionedMessageHandler(nfsServiceID, nfs4Version, nfs4ProcCompound, onNFSv4Compound)
}

type nfs4CompoundState struct {
	current *nfs4FileHandle
	saved   *nfs4FileHandle
}

type nfs4FileHandle struct {
	handle []byte
	fs     billy.Filesystem
	path   []string
}

type nfs4Result struct {
	op     nfs4Op
	status nfs4Status
	body   []byte
}

type nfs4FAttr struct {
	mask []uint32
	vals []byte
}

type nfs4SetAttrs struct {
	attrs SetFileAttributes
	mask  []uint32
}

func onNFSv4Compound(ctx context.Context, w *response, userHandle Handler) error {
	rd := newNFS4Reader(w.req.Body)
	tag, err := rd.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return writeNFSv4CompoundError(w, nfs4ErrBadXDR, nil)
	}

	minor, err := rd.readUint32()
	if err != nil {
		return writeNFSv4CompoundError(w, nfs4ErrBadXDR, tag)
	}
	opCount, err := rd.readUint32()
	if err != nil {
		return writeNFSv4CompoundError(w, nfs4ErrBadXDR, tag)
	}
	if minor != 0 {
		return writeNFSv4CompoundResponse(w, nfs4ErrMinorVersMismatch, tag, nil)
	}

	state := &nfs4CompoundState{}
	results := make([]nfs4Result, 0, opCount)
	compoundStatus := nfs4OK

	for i := uint32(0); i < opCount; i++ {
		opNum, err := rd.readUint32()
		if err != nil {
			results = append(results, nfs4Result{op: opIllegal, status: nfs4ErrBadXDR})
			compoundStatus = nfs4ErrBadXDR
			break
		}

		op := nfs4Op(opNum)
		res := executeNFSv4Op(ctx, rd, w, userHandle, state, op)
		results = append(results, res)
		if res.status != nfs4OK {
			compoundStatus = res.status
			break
		}
	}

	return writeNFSv4CompoundResponse(w, compoundStatus, tag, results)
}

func executeNFSv4Op(ctx context.Context, rd *nfs4Reader, w *response, userHandle Handler, state *nfs4CompoundState, op nfs4Op) nfs4Result {
	var body bytes.Buffer
	wr := newNFS4Writer(&body)
	var status nfs4Status

	switch op {
	case opAccess:
		status = nfs4OpAccess(rd, wr, state)
	case opClose:
		status = nfs4OpClose(rd, wr)
	case opCommit:
		status = nfs4OpCommit(rd, wr, w)
	case opCreate:
		status = nfs4OpCreate(rd, wr, userHandle, state)
	case opGetAttr:
		status = nfs4OpGetAttr(rd, wr, userHandle, state)
	case opGetFH:
		status = nfs4OpGetFH(wr, state)
	case opLink:
		status = nfs4ErrNotSupp
	case opLock:
		status = nfs4OpLock(rd, wr, w, state)
	case opLockT:
		status = nfs4OpLockT(rd, wr, w, state)
	case opLockU:
		status = nfs4OpLockU(rd, wr, w, state)
	case opLookup:
		status = nfs4OpLookup(rd, userHandle, state)
	case opLookupP:
		status = nfs4OpLookupParent(userHandle, state)
	case opOpen:
		status = nfs4OpOpen(rd, wr, userHandle, state)
	case opOpenConfirm:
		status = nfs4OpOpenConfirm(rd, wr)
	case opOpenDowngrade:
		status = nfs4OpOpenDowngrade(rd, wr)
	case opPutFH:
		status = nfs4OpPutFH(rd, userHandle, state)
	case opPutPubFH, opPutRootFH:
		status = nfs4OpPutRootFH(ctx, w.conn, userHandle, state)
	case opRead:
		status = nfs4OpRead(rd, wr, state)
	case opReadDir:
		status = nfs4OpReadDir(rd, wr, userHandle, state)
	case opReadLink:
		status = nfs4OpReadLink(wr, state)
	case opRemove:
		status = nfs4OpRemove(rd, wr, state)
	case opRename:
		status = nfs4OpRename(rd, wr, state)
	case opRenew:
		status = nfs4OpRenew(rd, w)
	case opRestoreFH:
		status = nfs4OpRestoreFH(state)
	case opSaveFH:
		status = nfs4OpSaveFH(state)
	case opSecInfo:
		status = nfs4OpSecInfo(rd, wr)
	case opSetAttr:
		status = nfs4OpSetAttr(rd, wr, userHandle, state)
	case opSetClientID:
		status = nfs4OpSetClientID(rd, wr)
	case opSetClientIDConfirm:
		status = nfs4OpSetClientIDConfirm(rd)
	case opWrite:
		status = nfs4OpWrite(rd, wr, w, state)
	case opReleaseLockOwner:
		status = nfs4OpReleaseLockOwner(rd, w)
	default:
		op = opIllegal
		status = nfs4ErrOpIllegal
	}

	if status != nfs4OK {
		Log.Debugf("nfs4 op %d returned status %d", op, status)
		// DENIED responses for LOCK/LOCKT carry the conflicting lock
		// details; every other failure has an empty body.
		if op != opLock && op != opLockT {
			body.Reset()
		}
	}
	return nfs4Result{op: op, status: status, body: body.Bytes()}
}

func nfs4OpAccess(rd *nfs4Reader, wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	req, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, status := state.requireCurrent(); status != nfs4OK {
		return status
	}
	supported := access4Read | access4Lookup | access4Modify | access4Extend | access4Delete | access4Execute
	wr.writeUint32(supported)
	wr.writeUint32(req & supported)
	return nfs4OK
}

func nfs4OpClose(rd *nfs4Reader, wr *nfs4Writer) nfs4Status {
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	stateID, err := rd.readFixedOpaque(16)
	if err != nil {
		return nfs4ErrBadXDR
	}
	wr.writeFixedOpaque(stateID)
	return nfs4OK
}

func nfs4OpCommit(rd *nfs4Reader, wr *nfs4Writer, w *response) nfs4Status {
	if _, err := rd.readUint64(); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	wr.writeFixedOpaque(w.Server.ID[:])
	return nfs4OK
}

func nfs4OpCreate(rd *nfs4Reader, wr *nfs4Writer, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	parent, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}

	objType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	switch objType {
	case nf4Dir:
	case nf4Lnk:
		if _, err := rd.readOpaque(nfs4OpaqueLimit); err != nil {
			return nfs4ErrBadXDR
		}
		return nfs4ErrNotSupp
	case nf4Blk, nf4Chr:
		if _, err := rd.readUint32(); err != nil {
			return nfs4ErrBadXDR
		}
		if _, err := rd.readUint32(); err != nil {
			return nfs4ErrBadXDR
		}
		return nfs4ErrNotSupp
	case nf4Sock, nf4FIFO:
		return nfs4ErrNotSupp
	default:
		return nfs4ErrBadType
	}

	name, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}
	attrs, status := readNFSv4SetAttrs(rd)
	if status != nfs4OK {
		return status
	}

	if status := ensureDirectory(parent); status != nfs4OK {
		return status
	}
	childPath := appendPath(parent.path, name)
	fullPath := nfs4Join(parent.fs, childPath)
	before := nfs4ChangeID(parent.fs, parent.path)
	if err := parent.fs.MkdirAll(fullPath, attrs.attrs.Mode(0777)); err != nil {
		return mapErrToNFS4Status(err)
	}
	after := nfs4ChangeID(parent.fs, parent.path)
	childHandle := userHandle.ToHandle(parent.fs, childPath)
	if len(childHandle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	state.current = &nfs4FileHandle{handle: childHandle, fs: parent.fs, path: childPath}

	writeChangeInfo(wr, false, before, after)
	writeBitmap(wr, attrs.mask)
	return nfs4OK
}

func nfs4OpGetAttr(rd *nfs4Reader, wr *nfs4Writer, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	mask, err := rd.readBitmap()
	if err != nil {
		return nfs4ErrBadXDR
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}

	info, err := current.fs.Lstat(nfs4Join(current.fs, current.path))
	if err != nil {
		return mapErrToNFS4Status(err)
	}

	attr, status := buildNFSv4Attrs(context.Background(), userHandle, current, info, mask)
	if status != nfs4OK {
		return status
	}
	writeFAttr(wr, attr)
	return nfs4OK
}

func nfs4OpGetFH(wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	wr.writeOpaque(current.handle)
	return nfs4OK
}

func nfs4OpLookup(rd *nfs4Reader, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	name, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := ensureDirectory(current); status != nfs4OK {
		return status
	}

	var childPath []string
	switch name {
	case ".":
		childPath = copyPath(current.path)
	case "..":
		if len(current.path) == 0 {
			return nfs4ErrNoEnt
		}
		childPath = copyPath(current.path[:len(current.path)-1])
	default:
		childPath = appendPath(current.path, name)
	}
	if _, err := current.fs.Lstat(nfs4Join(current.fs, childPath)); err != nil {
		return mapErrToNFS4Status(err)
	}
	handle := userHandle.ToHandle(current.fs, childPath)
	if len(handle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	state.current = &nfs4FileHandle{handle: handle, fs: current.fs, path: childPath}
	return nfs4OK
}

func nfs4OpLookupParent(userHandle Handler, state *nfs4CompoundState) nfs4Status {
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if len(current.path) == 0 {
		return nfs4ErrNoEnt
	}
	parentPath := copyPath(current.path[:len(current.path)-1])
	handle := userHandle.ToHandle(current.fs, parentPath)
	if len(handle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	state.current = &nfs4FileHandle{handle: handle, fs: current.fs, path: parentPath}
	return nfs4OK
}

func nfs4OpOpen(rd *nfs4Reader, wr *nfs4Writer, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	seqid, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	shareAccess, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	shareDeny, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint64(); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readOpaque(nfs4OpaqueLimit); err != nil {
		return nfs4ErrBadXDR
	}

	openType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	createMode := uint32(0)
	createAttrs := nfs4SetAttrs{}
	if openType == open4Create {
		createMode, err = rd.readUint32()
		if err != nil {
			return nfs4ErrBadXDR
		}
		switch createMode {
		case open4Unchecked, open4Guarded:
			attrs, status := readNFSv4SetAttrs(rd)
			if status != nfs4OK {
				return status
			}
			createAttrs = attrs
		case open4Exclusive:
			if _, err := rd.readFixedOpaque(nfs4VerifierSize); err != nil {
				return nfs4ErrBadXDR
			}
		default:
			return nfs4ErrInval
		}
	} else if openType != open4NoCreate {
		return nfs4ErrInval
	}

	claim, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if claim != claimNull {
		return nfs4ErrNotSupp
	}
	name, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}

	parent, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := ensureDirectory(parent); status != nfs4OK {
		return status
	}
	if shareDeny != open4ShareDenyNone {
		return nfs4ErrNotSupp
	}
	if shareAccess&(open4ShareAccessRead|open4ShareAccessWrite) == 0 {
		return nfs4ErrInval
	}

	childPath := appendPath(parent.path, name)
	fullPath := nfs4Join(parent.fs, childPath)
	before := nfs4ChangeID(parent.fs, parent.path)
	if openType == open4Create {
		if _, err := parent.fs.Lstat(fullPath); err == nil && createMode == open4Guarded {
			return nfs4ErrExist
		}
		flags := os.O_RDWR | os.O_CREATE
		if bitmapHas(createAttrs.mask, fattr4Size) && createAttrs.attrs.SetSize != nil && *createAttrs.attrs.SetSize == 0 {
			flags |= os.O_TRUNC
		}
		file, err := parent.fs.OpenFile(fullPath, flags, createAttrs.attrs.Mode(0666))
		if err != nil {
			return mapErrToNFS4Status(err)
		}
		if err := file.Close(); err != nil {
			return mapErrToNFS4Status(err)
		}
		if err := createAttrs.attrs.Apply(userHandle.Change(parent.fs), parent.fs, fullPath); err != nil {
			return mapErrToNFS4Status(err)
		}
	} else if _, err := parent.fs.Lstat(fullPath); err != nil {
		return mapErrToNFS4Status(err)
	}
	after := nfs4ChangeID(parent.fs, parent.path)

	handle := userHandle.ToHandle(parent.fs, childPath)
	if len(handle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	state.current = &nfs4FileHandle{handle: handle, fs: parent.fs, path: childPath}

	wr.writeFixedOpaque(makeStateID(seqid, childPath))
	writeChangeInfo(wr, false, before, after)
	wr.writeUint32(open4ResultLocktypePosix) // rflags
	writeBitmap(wr, createAttrs.mask)
	wr.writeUint32(openDelegateNone)
	return nfs4OK
}

func nfs4OpOpenConfirm(rd *nfs4Reader, wr *nfs4Writer) nfs4Status {
	stateID, err := rd.readFixedOpaque(16)
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	wr.writeFixedOpaque(stateID)
	return nfs4OK
}

func nfs4OpOpenDowngrade(rd *nfs4Reader, wr *nfs4Writer) nfs4Status {
	stateID, err := rd.readFixedOpaque(16)
	if err != nil {
		return nfs4ErrBadXDR
	}
	for i := 0; i < 3; i++ {
		if _, err := rd.readUint32(); err != nil {
			return nfs4ErrBadXDR
		}
	}
	wr.writeFixedOpaque(stateID)
	return nfs4OK
}

func nfs4OpPutFH(rd *nfs4Reader, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	fh, err := rd.readOpaque(nfs4FhSize)
	if err != nil {
		return nfs4ErrBadHandle
	}
	fs, p, err := userHandle.FromHandle(fh)
	if err != nil {
		return nfs4ErrStale
	}
	state.current = &nfs4FileHandle{handle: copyBytes(fh), fs: fs, path: copyPath(p)}
	return nfs4OK
}

func nfs4OpPutRootFH(ctx context.Context, conn net.Conn, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	status, fs, _ := userHandle.Mount(ctx, conn, MountRequest{Dirpath: []byte("/")})
	if status != MountStatusOk || fs == nil {
		return nfs4ErrAccess
	}
	handle := userHandle.ToHandle(fs, []string{})
	if len(handle) > nfs4FhSize {
		return nfs4ErrServerFault
	}
	state.current = &nfs4FileHandle{handle: handle, fs: fs, path: []string{}}
	return nfs4OK
}

func nfs4OpRead(rd *nfs4Reader, wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	if _, err := rd.readFixedOpaque(16); err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	count, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if count > nfs4MaxRead {
		count = nfs4MaxRead
	}
	file, err := current.fs.Open(nfs4Join(current.fs, current.path))
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	defer file.Close()

	data := make([]byte, count)
	n, err := file.ReadAt(data, int64(offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return mapErrToNFS4Status(err)
	}
	data = data[:n]
	eof := errors.Is(err, io.EOF)
	if !eof {
		if info, statErr := current.fs.Stat(nfs4Join(current.fs, current.path)); statErr == nil {
			eof = int64(offset)+int64(n) >= info.Size()
		}
	}
	wr.writeBool(eof)
	wr.writeOpaque(data)
	return nfs4OK
}

func nfs4OpReadDir(rd *nfs4Reader, wr *nfs4Writer, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	cookie, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	cookieVerf, err := rd.readFixedOpaque(nfs4VerifierSize)
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	maxCount, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	attrMask, err := rd.readBitmap()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if maxCount < 128 {
		return nfs4ErrTooSmall
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := ensureDirectory(current); status != nfs4OK {
		return status
	}
	verifier := binary.BigEndian.Uint64(cookieVerf)
	contents, actualVerifier, nfsErr := getDirListingWithVerifier(userHandle, current.handle, verifier)
	if nfsErr != nil {
		return mapErrToNFS4Status(nfsErr)
	}
	if cookie > 0 && verifier > 0 && verifier != actualVerifier {
		return nfs4ErrBadCookie
	}
	sort.Slice(contents, func(i, j int) bool {
		return contents[i].Name() < contents[j].Name()
	})

	wr.writeFixedOpaque(binary.BigEndian.AppendUint64(nil, actualVerifier))

	start := 0
	if cookie > 1 {
		if cookie > uint64(len(contents))+1 {
			return nfs4ErrBadCookie
		}
		start = int(cookie - 1)
	}

	entryBuf := bytes.NewBuffer(nil)
	eof := true
	emitted := 0
	for i := start; i < len(contents); i++ {
		entryInfo := contents[i]
		entryPath := appendPath(current.path, entryInfo.Name())
		entryHandle := userHandle.ToHandle(current.fs, entryPath)
		entryCurrent := &nfs4FileHandle{handle: entryHandle, fs: current.fs, path: entryPath}
		attrs, status := buildNFSv4Attrs(context.Background(), userHandle, entryCurrent, entryInfo, attrMask)
		if status != nfs4OK {
			return status
		}

		one := bytes.NewBuffer(nil)
		oneWriter := newNFS4Writer(one)
		oneWriter.writeUint64(uint64(i + 2))
		oneWriter.writeOpaque([]byte(entryInfo.Name()))
		writeFAttr(oneWriter, attrs)
		oneWriter.writeBool(true)

		if entryBuf.Len()+one.Len()+16 > int(maxCount) {
			if emitted == 0 {
				return nfs4ErrTooSmall
			}
			eof = false
			break
		}
		entryBuf.Write(one.Bytes())
		emitted++
	}

	if emitted == 0 {
		wr.writeBool(false)
	} else {
		wr.writeBool(true)
		data := entryBuf.Bytes()
		if len(data) >= 4 {
			binary.BigEndian.PutUint32(data[len(data)-4:], 0)
		}
		wr.writeBytes(data)
	}
	wr.writeBool(eof)
	return nfs4OK
}

func nfs4OpReadLink(wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	target, err := current.fs.Readlink(nfs4Join(current.fs, current.path))
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	wr.writeOpaque([]byte(target))
	return nfs4OK
}

func nfs4OpRemove(rd *nfs4Reader, wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	name, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := ensureDirectory(current); status != nfs4OK {
		return status
	}
	before := nfs4ChangeID(current.fs, current.path)
	err = current.fs.Remove(nfs4Join(current.fs, appendPath(current.path, name)))
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	after := nfs4ChangeID(current.fs, current.path)
	writeChangeInfo(wr, false, before, after)
	return nfs4OK
}

func nfs4OpRename(rd *nfs4Reader, wr *nfs4Writer, state *nfs4CompoundState) nfs4Status {
	if state.saved == nil {
		return nfs4ErrNoFileHandle
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	oldName, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}
	newName, err := rd.readComponent()
	if err != nil {
		return nfs4ErrBadName
	}
	if status := ensureDirectory(state.saved); status != nfs4OK {
		return status
	}
	if status := ensureDirectory(current); status != nfs4OK {
		return status
	}
	sourceBefore := nfs4ChangeID(state.saved.fs, state.saved.path)
	targetBefore := nfs4ChangeID(current.fs, current.path)
	oldPath := nfs4Join(state.saved.fs, appendPath(state.saved.path, oldName))
	newPath := nfs4Join(current.fs, appendPath(current.path, newName))
	if err := state.saved.fs.Rename(oldPath, newPath); err != nil {
		return mapErrToNFS4Status(err)
	}
	sourceAfter := nfs4ChangeID(state.saved.fs, state.saved.path)
	targetAfter := nfs4ChangeID(current.fs, current.path)
	writeChangeInfo(wr, false, sourceBefore, sourceAfter)
	writeChangeInfo(wr, false, targetBefore, targetAfter)
	return nfs4OK
}

func nfs4OpRenew(rd *nfs4Reader, w *response) nfs4Status {
	clientID, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	w.Server.lockManager().renewClient(clientID)
	return nfs4OK
}

func nfs4OpRestoreFH(state *nfs4CompoundState) nfs4Status {
	if state.saved == nil {
		return nfs4ErrNoFileHandle
	}
	state.current = state.saved.clone()
	return nfs4OK
}

func nfs4OpSaveFH(state *nfs4CompoundState) nfs4Status {
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	state.saved = current.clone()
	return nfs4OK
}

func nfs4OpSecInfo(rd *nfs4Reader, wr *nfs4Writer) nfs4Status {
	if _, err := rd.readComponent(); err != nil {
		return nfs4ErrBadName
	}
	wr.writeUint32(2)
	wr.writeUint32(authFlavorUnix)
	wr.writeUint32(authFlavorNull)
	return nfs4OK
}

func nfs4OpSetAttr(rd *nfs4Reader, wr *nfs4Writer, userHandle Handler, state *nfs4CompoundState) nfs4Status {
	if _, err := rd.readFixedOpaque(16); err != nil {
		return nfs4ErrBadXDR
	}
	attrs, status := readNFSv4SetAttrs(rd)
	if status != nfs4OK {
		writeBitmap(wr, nil)
		return status
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		writeBitmap(wr, nil)
		return status
	}
	if err := attrs.attrs.Apply(userHandle.Change(current.fs), current.fs, nfs4Join(current.fs, current.path)); err != nil {
		writeBitmap(wr, nil)
		return mapErrToNFS4Status(err)
	}
	writeBitmap(wr, attrs.mask)
	return nfs4OK
}

func nfs4OpSetClientID(rd *nfs4Reader, wr *nfs4Writer) nfs4Status {
	verifier, err := rd.readFixedOpaque(nfs4VerifierSize)
	if err != nil {
		return nfs4ErrBadXDR
	}
	id, err := rd.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readOpaque(nfs4OpaqueLimit); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readOpaque(nfs4OpaqueLimit); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil {
		return nfs4ErrBadXDR
	}
	sum := sha256.Sum256(append(verifier, id...))
	wr.writeUint64(binary.BigEndian.Uint64(sum[:8]))
	wr.writeFixedOpaque(sum[8:16])
	return nfs4OK
}

func nfs4OpSetClientIDConfirm(rd *nfs4Reader) nfs4Status {
	if _, err := rd.readUint64(); err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readFixedOpaque(nfs4VerifierSize); err != nil {
		return nfs4ErrBadXDR
	}
	return nfs4OK
}

func nfs4OpWrite(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	if _, err := rd.readFixedOpaque(16); err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	stable, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	data, err := rd.readOpaque(nfs4MaxWrite)
	if err != nil {
		return nfs4ErrBadXDR
	}
	if stable > uint32(fileSync) {
		return nfs4ErrInval
	}
	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if len(data) > math.MaxInt32 {
		return nfs4ErrFBig
	}
	info, err := current.fs.Stat(nfs4Join(current.fs, current.path))
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	if !info.Mode().IsRegular() {
		return nfs4ErrInval
	}
	file, err := current.fs.OpenFile(nfs4Join(current.fs, current.path), os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	if offset > 0 {
		if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
			_ = file.Close()
			return mapErrToNFS4Status(err)
		}
	}
	n, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return mapErrToNFS4Status(err)
	}
	if err := file.Close(); err != nil {
		return mapErrToNFS4Status(err)
	}
	wr.writeUint32(uint32(n))
	wr.writeUint32(uint32(fileSync))
	wr.writeFixedOpaque(w.Server.ID[:])
	return nfs4OK
}

func nfs4OpReleaseLockOwner(rd *nfs4Reader, w *response) nfs4Status {
	clientID, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	ownerBytes, err := rd.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return nfs4ErrBadXDR
	}
	w.Server.lockManager().releaseOwner(lockOwnerID{clientID: clientID, owner: string(ownerBytes)})
	return nfs4OK
}

func (s *nfs4CompoundState) requireCurrent() (*nfs4FileHandle, nfs4Status) {
	if s.current == nil {
		return nil, nfs4ErrNoFileHandle
	}
	return s.current, nfs4OK
}

func (fh *nfs4FileHandle) clone() *nfs4FileHandle {
	if fh == nil {
		return nil
	}
	return &nfs4FileHandle{
		handle: copyBytes(fh.handle),
		fs:     fh.fs,
		path:   copyPath(fh.path),
	}
}

func ensureDirectory(fh *nfs4FileHandle) nfs4Status {
	info, err := fh.fs.Lstat(nfs4Join(fh.fs, fh.path))
	if err != nil {
		return mapErrToNFS4Status(err)
	}
	if !info.IsDir() {
		return nfs4ErrNotDir
	}
	return nfs4OK
}

func writeNFSv4CompoundError(w *response, status nfs4Status, tag []byte) error {
	return writeNFSv4CompoundResponse(w, status, tag, nil)
}

func writeNFSv4CompoundResponse(w *response, status nfs4Status, tag []byte, results []nfs4Result) error {
	var body bytes.Buffer
	wr := newNFS4Writer(&body)
	wr.writeUint32(uint32(status))
	wr.writeOpaque(tag)
	wr.writeUint32(uint32(len(results)))
	for _, res := range results {
		wr.writeUint32(uint32(res.op))
		wr.writeUint32(uint32(res.status))
		// Failed ops have empty bodies except LOCK/LOCKT DENIED, whose
		// results must carry the LOCK4denied conflict details; the dispatch
		// layer already cleared bodies that must not be sent.
		if len(res.body) > 0 {
			wr.writeBytes(res.body)
		}
	}
	return w.Write(body.Bytes())
}

func buildNFSv4Attrs(ctx context.Context, userHandle Handler, current *nfs4FileHandle, info os.FileInfo, request []uint32) (nfs4FAttr, nfs4Status) {
	attr := ToFileAttribute(info, nfs4Join(current.fs, current.path))
	resultMask := make([]uint32, len(request))
	var vals bytes.Buffer
	wr := newNFS4Writer(&vals)
	for _, attrID := range bitmapAttrs(request) {
		if !bitmapHas(nfs4SupportedAttrBitmap, attrID) {
			continue
		}
		bitmapSet(&resultMask, attrID)
		switch attrID {
		case fattr4SupportedAttrs:
			writeBitmap(wr, nfs4SupportedAttrBitmap)
		case fattr4Type:
			wr.writeUint32(uint32(attr.Type))
		case fattr4FHExpireType:
			wr.writeUint32(0)
		case fattr4Change:
			wr.writeUint64(nfs4ChangeID(current.fs, current.path))
		case fattr4Size:
			wr.writeUint64(attr.Filesize)
		case fattr4LinkSupport:
			wr.writeBool(false)
		case fattr4SymlinkSupport:
			wr.writeBool(false)
		case fattr4NamedAttr:
			wr.writeBool(false)
		case fattr4FSID:
			wr.writeUint64(0)
			wr.writeUint64(1)
		case fattr4UniqueHandles:
			wr.writeBool(true)
		case fattr4LeaseTime:
			wr.writeUint32(nfs4LeaseTimeSecs)
		case fattr4RDAttrError:
			wr.writeUint32(uint32(nfs4OK))
		case fattr4ACLSupport:
			wr.writeUint32(0)
		case fattr4CanSetTime:
			wr.writeBool(userHandle.Change(current.fs) != nil)
		case fattr4CaseInsensitive:
			wr.writeBool(false)
		case fattr4CasePreserving:
			wr.writeBool(true)
		case fattr4ChownRestricted:
			wr.writeBool(true)
		case fattr4FileHandle:
			wr.writeOpaque(current.handle)
		case fattr4FileID:
			wr.writeUint64(attr.Fileid)
		case fattr4FilesAvail, fattr4FilesFree:
			wr.writeUint64(nfs4FSStat(ctx, userHandle, current).AvailableFiles)
		case fattr4FilesTotal:
			wr.writeUint64(nfs4FSStat(ctx, userHandle, current).TotalFiles)
		case fattr4Hidden:
			wr.writeBool(false)
		case fattr4Homogeneous:
			wr.writeBool(true)
		case fattr4MaxFileSize:
			wr.writeUint64(math.MaxInt64)
		case fattr4MaxLink:
			wr.writeUint32(1)
		case fattr4MaxName:
			wr.writeUint32(255)
		case fattr4MaxRead:
			wr.writeUint64(nfs4MaxRead)
		case fattr4MaxWrite:
			wr.writeUint64(nfs4MaxWrite)
		case fattr4Mode:
			wr.writeUint32(uint32(info.Mode().Perm()))
		case fattr4NoTrunc:
			wr.writeBool(true)
		case fattr4NumLinks:
			wr.writeUint32(attr.Nlink)
		case fattr4Owner:
			wr.writeOpaque([]byte(strconv.Itoa(int(attr.UID))))
		case fattr4OwnerGroup:
			wr.writeOpaque([]byte(strconv.Itoa(int(attr.GID))))
		case fattr4RawDev:
			wr.writeUint32(attr.SpecData[0])
			wr.writeUint32(attr.SpecData[1])
		case fattr4SpaceAvail, fattr4SpaceFree:
			wr.writeUint64(nfs4FSStat(ctx, userHandle, current).AvailableSize)
		case fattr4SpaceTotal:
			wr.writeUint64(nfs4FSStat(ctx, userHandle, current).TotalSize)
		case fattr4SpaceUsed:
			wr.writeUint64(attr.Used)
		case fattr4System:
			wr.writeBool(false)
		case fattr4TimeAccess, fattr4TimeMetadata, fattr4TimeModify:
			writeNFSv4Time(wr, attr.Mtime)
		case fattr4TimeDelta:
			wr.writeInt64(0)
			wr.writeUint32(1)
		case fattr4MountedOnFileID:
			wr.writeUint64(attr.Fileid)
		default:
			bitmapClear(&resultMask, attrID)
		}
	}
	return nfs4FAttr{mask: trimBitmap(resultMask), vals: vals.Bytes()}, nfs4OK
}

func readNFSv4SetAttrs(rd *nfs4Reader) (nfs4SetAttrs, nfs4Status) {
	mask, err := rd.readBitmap()
	if err != nil {
		return nfs4SetAttrs{}, nfs4ErrBadXDR
	}
	raw, err := rd.readOpaque(1 << 20)
	if err != nil {
		return nfs4SetAttrs{}, nfs4ErrBadXDR
	}

	attrs := nfs4SetAttrs{mask: trimBitmap(mask)}
	attrReader := newNFS4Reader(bytes.NewReader(raw))
	for _, attrID := range bitmapAttrs(mask) {
		if !containsAttr(nfs4WriteAttrIDs, attrID) {
			return nfs4SetAttrs{}, nfs4ErrAttrNotSupp
		}
		switch attrID {
		case fattr4Size:
			size, err := attrReader.readUint64()
			if err != nil {
				return nfs4SetAttrs{}, nfs4ErrBadXDR
			}
			attrs.attrs.SetSize = &size
		case fattr4Mode:
			mode, err := attrReader.readUint32()
			if err != nil {
				return nfs4SetAttrs{}, nfs4ErrBadXDR
			}
			attrs.attrs.SetMode = &mode
		case fattr4TimeAccessSet:
			tm, status := readNFSv4SetTime(attrReader)
			if status != nfs4OK {
				return nfs4SetAttrs{}, status
			}
			attrs.attrs.SetAtime = tm
		case fattr4TimeModifySet:
			tm, status := readNFSv4SetTime(attrReader)
			if status != nfs4OK {
				return nfs4SetAttrs{}, status
			}
			attrs.attrs.SetMtime = tm
		}
	}
	return attrs, nfs4OK
}

func readNFSv4SetTime(rd *nfs4Reader) (*time.Time, nfs4Status) {
	setIt, err := rd.readUint32()
	if err != nil {
		return nil, nfs4ErrBadXDR
	}
	switch setIt {
	case 0:
		now := time.Now()
		return &now, nfs4OK
	case 1:
		seconds, err := rd.readInt64()
		if err != nil {
			return nil, nfs4ErrBadXDR
		}
		nseconds, err := rd.readUint32()
		if err != nil {
			return nil, nfs4ErrBadXDR
		}
		tm := time.Unix(seconds, int64(nseconds))
		return &tm, nfs4OK
	default:
		return nil, nfs4ErrInval
	}
}

func nfs4FSStat(ctx context.Context, userHandle Handler, current *nfs4FileHandle) FSStat {
	stat := FSStat{
		TotalSize:      1 << 50,
		FreeSize:       1 << 50,
		AvailableSize:  1 << 50,
		TotalFiles:     1 << 32,
		FreeFiles:      1 << 32,
		AvailableFiles: 1 << 32,
	}
	_ = userHandle.FSStat(ctx, current.fs, &stat)
	return stat
}

func writeFAttr(wr *nfs4Writer, attr nfs4FAttr) {
	writeBitmap(wr, attr.mask)
	wr.writeOpaque(attr.vals)
}

func writeChangeInfo(wr *nfs4Writer, atomic bool, before uint64, after uint64) {
	wr.writeBool(atomic)
	wr.writeUint64(before)
	wr.writeUint64(after)
}

func writeNFSv4Time(wr *nfs4Writer, t FileTime) {
	wr.writeInt64(int64(t.Seconds))
	wr.writeUint32(t.Nseconds)
}

func makeStateID(seqid uint32, path []string) []byte {
	stateID := make([]byte, 16)
	binary.BigEndian.PutUint32(stateID[0:4], seqid)
	sum := sha256.Sum256([]byte(stringsJoinPath(path)))
	copy(stateID[4:], sum[:nfs4OtherSize])
	return stateID
}

func nfs4ChangeID(fs billy.Filesystem, p []string) uint64 {
	joined := nfs4Join(fs, p)
	info, err := fs.Lstat(joined)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(joined))
	_ = binary.Write(h, binary.BigEndian, info.Size())
	_ = binary.Write(h, binary.BigEndian, info.ModTime().UnixNano())
	return h.Sum64()
}

func mapErrToNFS4Status(err error) nfs4Status {
	if err == nil {
		return nfs4OK
	}
	var nfsErr *NFSStatusError
	if errors.As(err, &nfsErr) {
		return nfsStatus3To4(nfsErr.NFSStatus)
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

func nfsStatus3To4(status NFSStatus) nfs4Status {
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

func bitmapFromAttrs(attrs ...uint32) []uint32 {
	var bm []uint32
	for _, attr := range attrs {
		bitmapSet(&bm, attr)
	}
	return trimBitmap(bm)
}

func bitmapHas(bm []uint32, attr uint32) bool {
	word := int(attr / 32)
	bit := attr % 32
	return word < len(bm) && (bm[word]&(uint32(1)<<bit)) != 0
}

func bitmapSet(bm *[]uint32, attr uint32) {
	word := int(attr / 32)
	for len(*bm) <= word {
		*bm = append(*bm, 0)
	}
	(*bm)[word] |= uint32(1) << (attr % 32)
}

func bitmapClear(bm *[]uint32, attr uint32) {
	word := int(attr / 32)
	if word >= len(*bm) {
		return
	}
	(*bm)[word] &^= uint32(1) << (attr % 32)
}

func trimBitmap(bm []uint32) []uint32 {
	end := len(bm)
	for end > 0 && bm[end-1] == 0 {
		end--
	}
	out := make([]uint32, end)
	copy(out, bm[:end])
	return out
}

func bitmapAttrs(bm []uint32) []uint32 {
	var attrs []uint32
	for word, val := range bm {
		for bit := uint32(0); bit < 32; bit++ {
			if val&(uint32(1)<<bit) != 0 {
				attrs = append(attrs, uint32(word*32)+bit)
			}
		}
	}
	return attrs
}

func writeBitmap(wr *nfs4Writer, bm []uint32) {
	bm = trimBitmap(bm)
	wr.writeUint32(uint32(len(bm)))
	for _, word := range bm {
		wr.writeUint32(word)
	}
}

func containsAttr(attrs []uint32, attr uint32) bool {
	for _, candidate := range attrs {
		if candidate == attr {
			return true
		}
	}
	return false
}

func appendPath(base []string, elem string) []string {
	out := copyPath(base)
	if elem != "" {
		out = append(out, elem)
	}
	return out
}

func copyPath(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func stringsJoinPath(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return path.Join(parts...)
}

func nfs4Join(fs billy.Filesystem, parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return fs.Join(parts...)
}

type nfs4Reader struct {
	r io.Reader
}

func newNFS4Reader(r io.Reader) *nfs4Reader {
	return &nfs4Reader{r: r}
}

func (r *nfs4Reader) readUint32() (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r.r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

func (r *nfs4Reader) readUint64() (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r.r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(buf[:]), nil
}

func (r *nfs4Reader) readInt64() (int64, error) {
	v, err := r.readUint64()
	return int64(v), err
}

func (r *nfs4Reader) readBitmap() ([]uint32, error) {
	length, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	if length > 16 {
		return nil, fmt.Errorf("bitmap too large: %d", length)
	}
	bm := make([]uint32, length)
	for i := range bm {
		bm[i], err = r.readUint32()
		if err != nil {
			return nil, err
		}
	}
	return bm, nil
}

func (r *nfs4Reader) readOpaque(max uint32) ([]byte, error) {
	length, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	if length > max {
		return nil, fmt.Errorf("opaque length %d exceeds %d", length, max)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r.r, data); err != nil {
		return nil, err
	}
	padding := xdrPadding(length)
	if padding > 0 {
		var pad [3]byte
		if _, err := io.ReadFull(r.r, pad[:padding]); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (r *nfs4Reader) readComponent() (string, error) {
	name, err := r.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return "", err
	}
	if len(name) == 0 || bytes.Contains(name, []byte{0}) || bytes.Contains(name, []byte("/")) {
		return "", fmt.Errorf("invalid component")
	}
	return string(name), nil
}

func (r *nfs4Reader) readFixedOpaque(length uint32) ([]byte, error) {
	data := make([]byte, length)
	if _, err := io.ReadFull(r.r, data); err != nil {
		return nil, err
	}
	padding := xdrPadding(length)
	if padding > 0 {
		var pad [3]byte
		if _, err := io.ReadFull(r.r, pad[:padding]); err != nil {
			return nil, err
		}
	}
	return data, nil
}

type nfs4Writer struct {
	w io.Writer
}

func newNFS4Writer(w io.Writer) *nfs4Writer {
	return &nfs4Writer{w: w}
}

func (w *nfs4Writer) writeBytes(data []byte) {
	_, _ = w.w.Write(data)
}

func (w *nfs4Writer) writeUint32(v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	w.writeBytes(buf[:])
}

func (w *nfs4Writer) writeUint64(v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	w.writeBytes(buf[:])
}

func (w *nfs4Writer) writeInt64(v int64) {
	w.writeUint64(uint64(v))
}

func (w *nfs4Writer) writeBool(v bool) {
	if v {
		w.writeUint32(1)
		return
	}
	w.writeUint32(0)
}

func (w *nfs4Writer) writeOpaque(data []byte) {
	w.writeUint32(uint32(len(data)))
	w.writeBytes(data)
	w.writePadding(uint32(len(data)))
}

func (w *nfs4Writer) writeFixedOpaque(data []byte) {
	w.writeBytes(data)
	w.writePadding(uint32(len(data)))
}

func (w *nfs4Writer) writePadding(length uint32) {
	padding := xdrPadding(length)
	if padding == 0 {
		return
	}
	w.writeBytes(make([]byte, padding))
}

func xdrPadding(length uint32) uint32 {
	return (4 - (length % 4)) % 4
}
