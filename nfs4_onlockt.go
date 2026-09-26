package nfs

import "io"

type nfs4LockTArgs struct {
	LockType uint32
	Offset   uint64
	Length   uint64
	Owner    nfs4Owner
}

// nfs4OnLockT implements LOCKT (RFC 7530 section 16.11). A DENIED result
// carries the conflicting lock.
func nfs4OnLockT(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4LockTArgs
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
	denied, status := c.w.Server.nfs4State().test(req.Owner, current.fullPath(), r)
	if status == nfs4ErrDenied {
		if encoded := nfs4Encode(res, denied); encoded != nfs4OK {
			return encoded
		}
	}
	return status
}
