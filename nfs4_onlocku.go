package nfs

import "io"

type nfs4LockUArgs struct {
	LockType uint32
	Seqid    uint32
	StateID  nfs4StateID
	Offset   uint64
	Length   uint64
}

// nfs4OnLockU implements LOCKU (RFC 7530 section 16.12).
func nfs4OnLockU(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4LockUArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	r, status := nfs4LockRange(req.Offset, req.Length, req.LockType)
	if status != nfs4OK {
		return status
	}
	id, status := c.w.Server.nfs4State().unlock(req.StateID, current.fullPath(), r)
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, id)
}
