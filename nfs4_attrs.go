package nfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/fnv"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// nfs4Attr is an NFSv4 file attribute number (RFC 7530 section 5).
type nfs4Attr uint32

const (
	nfs4AttrSupportedAttrs  nfs4Attr = 0
	nfs4AttrType            nfs4Attr = 1
	nfs4AttrFHExpireType    nfs4Attr = 2
	nfs4AttrChange          nfs4Attr = 3
	nfs4AttrSize            nfs4Attr = 4
	nfs4AttrLinkSupport     nfs4Attr = 5
	nfs4AttrSymlinkSupport  nfs4Attr = 6
	nfs4AttrNamedAttr       nfs4Attr = 7
	nfs4AttrFSID            nfs4Attr = 8
	nfs4AttrUniqueHandles   nfs4Attr = 9
	nfs4AttrLeaseTime       nfs4Attr = 10
	nfs4AttrRDAttrError     nfs4Attr = 11
	nfs4AttrACLSupport      nfs4Attr = 13
	nfs4AttrCanSetTime      nfs4Attr = 15
	nfs4AttrCaseInsensitive nfs4Attr = 16
	nfs4AttrCasePreserving  nfs4Attr = 17
	nfs4AttrChownRestricted nfs4Attr = 18
	nfs4AttrFileHandle      nfs4Attr = 19
	nfs4AttrFileID          nfs4Attr = 20
	nfs4AttrFilesAvail      nfs4Attr = 21
	nfs4AttrFilesFree       nfs4Attr = 22
	nfs4AttrFilesTotal      nfs4Attr = 23
	nfs4AttrHidden          nfs4Attr = 25
	nfs4AttrHomogeneous     nfs4Attr = 26
	nfs4AttrMaxFileSize     nfs4Attr = 27
	nfs4AttrMaxLink         nfs4Attr = 28
	nfs4AttrMaxName         nfs4Attr = 29
	nfs4AttrMaxRead         nfs4Attr = 30
	nfs4AttrMaxWrite        nfs4Attr = 31
	nfs4AttrMode            nfs4Attr = 33
	nfs4AttrNoTrunc         nfs4Attr = 34
	nfs4AttrNumLinks        nfs4Attr = 35
	nfs4AttrOwner           nfs4Attr = 36
	nfs4AttrOwnerGroup      nfs4Attr = 37
	nfs4AttrRawDev          nfs4Attr = 41
	nfs4AttrSpaceAvail      nfs4Attr = 42
	nfs4AttrSpaceFree       nfs4Attr = 43
	nfs4AttrSpaceTotal      nfs4Attr = 44
	nfs4AttrSpaceUsed       nfs4Attr = 45
	nfs4AttrSystem          nfs4Attr = 46
	nfs4AttrTimeAccess      nfs4Attr = 47
	nfs4AttrTimeAccessSet   nfs4Attr = 48
	nfs4AttrTimeDelta       nfs4Attr = 51
	nfs4AttrTimeMetadata    nfs4Attr = 52
	nfs4AttrTimeModify      nfs4Attr = 53
	nfs4AttrTimeModifySet   nfs4Attr = 54
	nfs4AttrMountedOnFileID nfs4Attr = 55
)

var (
	// nfs4SupportedAttrs lists the write-only time_access_set and
	// time_modify_set too: clients only send the attributes a server
	// says it supports.
	nfs4SupportedAttrs = nfs4BitmapOf(
		nfs4AttrSupportedAttrs,
		nfs4AttrType,
		nfs4AttrFHExpireType,
		nfs4AttrChange,
		nfs4AttrSize,
		nfs4AttrLinkSupport,
		nfs4AttrSymlinkSupport,
		nfs4AttrNamedAttr,
		nfs4AttrFSID,
		nfs4AttrUniqueHandles,
		nfs4AttrLeaseTime,
		nfs4AttrRDAttrError,
		nfs4AttrACLSupport,
		nfs4AttrCanSetTime,
		nfs4AttrCaseInsensitive,
		nfs4AttrCasePreserving,
		nfs4AttrChownRestricted,
		nfs4AttrFileHandle,
		nfs4AttrFileID,
		nfs4AttrFilesAvail,
		nfs4AttrFilesFree,
		nfs4AttrFilesTotal,
		nfs4AttrHidden,
		nfs4AttrHomogeneous,
		nfs4AttrMaxFileSize,
		nfs4AttrMaxLink,
		nfs4AttrMaxName,
		nfs4AttrMaxRead,
		nfs4AttrMaxWrite,
		nfs4AttrMode,
		nfs4AttrNoTrunc,
		nfs4AttrNumLinks,
		nfs4AttrOwner,
		nfs4AttrOwnerGroup,
		nfs4AttrRawDev,
		nfs4AttrSpaceAvail,
		nfs4AttrSpaceFree,
		nfs4AttrSpaceTotal,
		nfs4AttrSpaceUsed,
		nfs4AttrSystem,
		nfs4AttrTimeAccess,
		nfs4AttrTimeAccessSet,
		nfs4AttrTimeDelta,
		nfs4AttrTimeMetadata,
		nfs4AttrTimeModify,
		nfs4AttrTimeModifySet,
		nfs4AttrMountedOnFileID,
	)
	nfs4WritableAttrs = nfs4BitmapOf(nfs4AttrSize, nfs4AttrMode, nfs4AttrTimeAccessSet, nfs4AttrTimeModifySet)
)

// nfs4Bitmap is a bitmap4 of attribute numbers.
type nfs4Bitmap []uint32

func nfs4BitmapOf(attrs ...nfs4Attr) nfs4Bitmap {
	var bm nfs4Bitmap
	for _, attr := range attrs {
		bm.set(attr)
	}
	return bm
}

func (bm nfs4Bitmap) has(attr nfs4Attr) bool {
	word := int(attr / 32)
	return word < len(bm) && bm[word]&(1<<(attr%32)) != 0
}

func (bm *nfs4Bitmap) set(attr nfs4Attr) {
	word := int(attr / 32)
	for len(*bm) <= word {
		*bm = append(*bm, 0)
	}
	(*bm)[word] |= 1 << (attr % 32)
}

// attrs lists the attributes set in the bitmap, in increasing order: the
// order their values take in a fattr4.
func (bm nfs4Bitmap) attrs() []nfs4Attr {
	var attrs []nfs4Attr
	for word, val := range bm {
		for bit := 0; val != 0; bit++ {
			if val&1 != 0 {
				attrs = append(attrs, nfs4Attr(word*32+bit))
			}
			val >>= 1
		}
	}
	return attrs
}

// nfs4FAttr is a fattr4: a bitmap and the XDR-encoded values it lists.
type nfs4FAttr struct {
	Mask nfs4Bitmap
	Vals []byte
}

type nfs4Time struct {
	Seconds  int64
	Nseconds uint32
}

type nfs4FSID struct {
	Major uint64
	Minor uint64
}

// nfs4BuildAttrs encodes the requested attributes of the file fh, whose
// stat is info, skipping those this server does not support.
func nfs4BuildAttrs(ctx context.Context, userHandle Handler, fh *nfs4FileHandle, info os.FileInfo, request nfs4Bitmap) (nfs4FAttr, nfs4Status) {
	attr := ToFileAttribute(info, fh.fullPath())
	var fsStat *FSStat
	stat := func() *FSStat {
		if fsStat == nil {
			fsStat = nfs4FSStat(ctx, userHandle, fh.fs)
		}
		return fsStat
	}

	var out nfs4FAttr
	var vals bytes.Buffer
	for _, id := range request.attrs() {
		if !nfs4SupportedAttrs.has(id) {
			continue
		}
		var v interface{}
		switch id {
		case nfs4AttrSupportedAttrs:
			v = nfs4SupportedAttrs
		case nfs4AttrType:
			v = uint32(attr.Type)
		case nfs4AttrFHExpireType:
			v = uint32(0) // FH4_PERSISTENT
		case nfs4AttrChange:
			v = fh.changeID()
		case nfs4AttrSize:
			v = attr.Filesize
		case nfs4AttrLinkSupport, nfs4AttrSymlinkSupport, nfs4AttrNamedAttr,
			nfs4AttrCaseInsensitive, nfs4AttrHidden, nfs4AttrSystem:
			v = false
		case nfs4AttrFSID:
			v = nfs4FSID{Major: 0, Minor: 1}
		case nfs4AttrUniqueHandles, nfs4AttrCasePreserving, nfs4AttrChownRestricted,
			nfs4AttrHomogeneous, nfs4AttrNoTrunc:
			v = true
		case nfs4AttrLeaseTime:
			v = uint32(nfs4LeaseTimeSecs)
		case nfs4AttrRDAttrError:
			v = nfs4OK
		case nfs4AttrACLSupport:
			v = uint32(0)
		case nfs4AttrCanSetTime:
			v = userHandle.Change(fh.fs) != nil
		case nfs4AttrFileHandle:
			v = fh.handle
		case nfs4AttrFileID, nfs4AttrMountedOnFileID:
			v = attr.Fileid
		case nfs4AttrFilesAvail, nfs4AttrFilesFree:
			v = stat().AvailableFiles
		case nfs4AttrFilesTotal:
			v = stat().TotalFiles
		case nfs4AttrMaxFileSize:
			v = uint64(math.MaxInt64)
		case nfs4AttrMaxLink:
			v = uint32(1)
		case nfs4AttrMaxName:
			v = uint32(255)
		case nfs4AttrMaxRead:
			v = uint64(nfs4MaxRead)
		case nfs4AttrMaxWrite:
			v = uint64(nfs4MaxWrite)
		case nfs4AttrMode:
			v = uint32(info.Mode().Perm())
		case nfs4AttrNumLinks:
			v = attr.Nlink
		case nfs4AttrOwner:
			v = strconv.Itoa(int(attr.UID))
		case nfs4AttrOwnerGroup:
			v = strconv.Itoa(int(attr.GID))
		case nfs4AttrRawDev:
			v = attr.SpecData
		case nfs4AttrSpaceAvail, nfs4AttrSpaceFree:
			v = stat().AvailableSize
		case nfs4AttrSpaceTotal:
			v = stat().TotalSize
		case nfs4AttrSpaceUsed:
			v = attr.Used
		case nfs4AttrTimeAccess, nfs4AttrTimeMetadata, nfs4AttrTimeModify:
			v = nfs4Time{Seconds: int64(attr.Mtime.Seconds), Nseconds: attr.Mtime.Nseconds}
		case nfs4AttrTimeDelta:
			v = nfs4Time{Seconds: 0, Nseconds: 1}
		default:
			continue
		}
		if err := xdr.Write(&vals, v); err != nil {
			return nfs4FAttr{}, nfs4ErrServerFault
		}
		out.Mask.set(id)
	}
	out.Vals = vals.Bytes()
	return out, nfs4OK
}

// nfs4SetAttrs is a decoded fattr4 of attributes to set.
type nfs4SetAttrs struct {
	attrs SetFileAttributes
	mask  nfs4Bitmap
}

// nfs4SetTime is a settime4.
type nfs4SetTime struct {
	How  uint32   `xdr:"union"`
	Time nfs4Time `xdr:"unioncase=1"` // SET_TO_CLIENT_TIME4
}

// nfs4DecodeSetAttrs reads the attributes a SETATTR, CREATE or OPEN sets.
func nfs4DecodeSetAttrs(fattr nfs4FAttr) (nfs4SetAttrs, nfs4Status) {
	attrs := nfs4SetAttrs{mask: fattr.Mask}
	vals := bytes.NewReader(fattr.Vals)
	for _, id := range fattr.Mask.attrs() {
		if !nfs4WritableAttrs.has(id) {
			return nfs4SetAttrs{}, nfs4ErrAttrNotSupp
		}
		switch id {
		case nfs4AttrSize:
			var size uint64
			if err := xdr.Read(vals, &size); err != nil {
				return nfs4SetAttrs{}, nfs4ErrBadXDR
			}
			attrs.attrs.SetSize = &size
		case nfs4AttrMode:
			var mode uint32
			if err := xdr.Read(vals, &mode); err != nil {
				return nfs4SetAttrs{}, nfs4ErrBadXDR
			}
			attrs.attrs.SetMode = &mode
		case nfs4AttrTimeAccessSet, nfs4AttrTimeModifySet:
			var st nfs4SetTime
			if err := xdr.Read(vals, &st); err != nil {
				return nfs4SetAttrs{}, nfs4ErrBadXDR
			}
			var tm time.Time
			switch st.How {
			case 0: // SET_TO_SERVER_TIME4
				tm = time.Now()
			case 1:
				tm = time.Unix(st.Time.Seconds, int64(st.Time.Nseconds))
			default:
				return nfs4SetAttrs{}, nfs4ErrInval
			}
			if id == nfs4AttrTimeAccessSet {
				attrs.attrs.SetAtime = &tm
			} else {
				attrs.attrs.SetMtime = &tm
			}
		}
	}
	return attrs, nfs4OK
}

func nfs4FSStat(ctx context.Context, userHandle Handler, fs billy.Filesystem) *FSStat {
	stat := FSStat{
		TotalSize:      1 << 50,
		FreeSize:       1 << 50,
		AvailableSize:  1 << 50,
		TotalFiles:     1 << 32,
		FreeFiles:      1 << 32,
		AvailableFiles: 1 << 32,
	}
	_ = userHandle.FSStat(ctx, fs, &stat)
	return &stat
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
