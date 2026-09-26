package nfs

import (
	"io"
	"os"
)

const (
	nfs4OpenNoCreate uint32 = 0
	nfs4OpenCreate   uint32 = 1

	nfs4CreateUnchecked uint32 = 0
	nfs4CreateGuarded   uint32 = 1
	nfs4CreateExclusive uint32 = 2

	nfs4ShareAccessRead  uint32 = 0x00000001
	nfs4ShareAccessWrite uint32 = 0x00000002
	nfs4ShareDenyNone    uint32 = 0

	nfs4ClaimNull     uint32 = 0
	nfs4ClaimPrevious uint32 = 1

	nfs4OpenDelegateNone uint32 = 0

	// nfs4OpenResultLocktypePosix in OPEN rflags advertises POSIX
	// byte-range lock semantics; without it the Linux client fails fcntl
	// locks locally with ENOLCK and never sends LOCK. (0x2 is
	// OPEN4_RESULT_CONFIRM, which this server never asks for.)
	nfs4OpenResultLocktypePosix uint32 = 0x4
)

// nfs4CreateHow is a createhow4: UNCHECKED4 and GUARDED4 carry the
// attributes to create the file with, EXCLUSIVE4 a verifier.
type nfs4CreateHow struct {
	Mode           uint32    `xdr:"union"`
	UncheckedAttrs nfs4FAttr `xdr:"unioncase=0"`
	GuardedAttrs   nfs4FAttr `xdr:"unioncase=1"`
	Verifier       [8]byte   `xdr:"unioncase=2"`
}

// nfs4OpenArgs is an OPEN4args. Of the open_claim4 cases only CLAIM_NULL,
// which names the file, is decoded.
type nfs4OpenArgs struct {
	Seqid       uint32
	ShareAccess uint32
	ShareDeny   uint32
	Owner       nfs4Owner
	OpenType    uint32        `xdr:"union"`
	How         nfs4CreateHow `xdr:"unioncase=1"`
	Claim       uint32        `xdr:"union"`
	File        string        `xdr:"unioncase=0"`
}

type nfs4OpenRes struct {
	StateID    nfs4StateID
	CInfo      nfs4ChangeInfo
	RFlags     uint32
	AttrSet    nfs4Bitmap
	Delegation uint32
}

func nfs4OnOpen(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4OpenArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	var createAttrs nfs4SetAttrs
	switch req.OpenType {
	case nfs4OpenNoCreate:
	case nfs4OpenCreate:
		var fattr nfs4FAttr
		switch req.How.Mode {
		case nfs4CreateUnchecked:
			fattr = req.How.UncheckedAttrs
		case nfs4CreateGuarded:
			fattr = req.How.GuardedAttrs
		case nfs4CreateExclusive:
			// EXCLUSIVE4 has the server keep the verifier with the file,
			// so a retransmitted OPEN finds its own file and succeeds.
			// Nowhere to keep it here, it is handled as GUARDED4: an
			// existing file fails with NFS4ERR_EXIST.
		default:
			return nfs4ErrInval
		}
		var status nfs4Status
		if createAttrs, status = nfs4DecodeSetAttrs(fattr); status != nfs4OK {
			return status
		}
	default:
		return nfs4ErrInval
	}
	if req.Claim == nfs4ClaimPrevious {
		// No grace period: this server keeps no open state across a
		// restart to reclaim. NFS4ERR_NO_GRACE sends the client to open
		// the file afresh; any other error loses the open.
		return nfs4ErrNoGrace
	}
	if req.Claim != nfs4ClaimNull {
		return nfs4ErrNotSupp
	}
	if status := nfs4Component(req.File); status != nfs4OK {
		return status
	}

	parent, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := parent.ensureDir(); status != nfs4OK {
		return status
	}
	if req.ShareDeny != nfs4ShareDenyNone {
		return nfs4ErrNotSupp
	}
	if req.ShareAccess&(nfs4ShareAccessRead|nfs4ShareAccessWrite) == 0 {
		return nfs4ErrInval
	}
	if req.OpenType == nfs4OpenCreate || req.ShareAccess&nfs4ShareAccessWrite != 0 {
		if status := parent.ensureWritable(); status != nfs4OK {
			return status
		}
	}

	sm := c.w.Server.nfs4State()
	if status := sm.checkClient(req.Owner.ClientID); status != nfs4OK {
		return status
	}
	childPath := parent.child(req.File)
	fullPath := nfs4Join(parent.fs, childPath)
	before := parent.changeID()
	if req.OpenType == nfs4OpenCreate {
		if req.How.Mode != nfs4CreateUnchecked {
			if _, err := parent.fs.Lstat(fullPath); err == nil {
				return nfs4ErrExist
			}
		}
		flags := os.O_RDWR | os.O_CREATE
		if size := createAttrs.attrs.SetSize; size != nil && *size == 0 {
			flags |= os.O_TRUNC
		}
		file, err := parent.fs.OpenFile(fullPath, flags, createAttrs.attrs.Mode(0666))
		if err != nil {
			return nfs4StatusFromErr(err)
		}
		if err := file.Close(); err != nil {
			return nfs4StatusFromErr(err)
		}
		if err := createAttrs.attrs.Apply(c.handler.Change(parent.fs), parent.fs, fullPath); err != nil {
			return nfs4StatusFromErr(err)
		}
	} else if _, err := parent.fs.Lstat(fullPath); err != nil {
		return nfs4StatusFromErr(err)
	}
	after := parent.changeID()

	if status := c.setCurrent(parent.fs, childPath); status != nfs4OK {
		return status
	}
	id, status := sm.open(req.Owner, fullPath)
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, nfs4OpenRes{
		StateID:    id,
		CInfo:      nfs4ChangeInfo{Before: before, After: after},
		RFlags:     nfs4OpenResultLocktypePosix,
		AttrSet:    createAttrs.mask,
		Delegation: nfs4OpenDelegateNone,
	})
}
