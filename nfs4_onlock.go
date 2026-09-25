package nfs

import "io"

// nfs4Locker is a locker4: a lock owner new to this file, introduced with
// the open it locks under, or the stateid of the owner's existing locks.
type nfs4Locker struct {
	NewLockOwner bool                `xdr:"union"`
	OpenOwner    nfs4OpenToLockOwner `xdr:"unioncase=1"`
	LockOwner    nfs4ExistLockOwner  `xdr:"unioncase=0"`
}

type nfs4OpenToLockOwner struct {
	OpenSeqid   uint32
	OpenStateID nfs4StateID
	LockSeqid   uint32
	LockOwner   nfs4Owner
}

type nfs4ExistLockOwner struct {
	LockStateID nfs4StateID
	LockSeqid   uint32
}

type nfs4LockArgs struct {
	LockType uint32
	Reclaim  bool
	Offset   uint64
	Length   uint64
	Locker   nfs4Locker
}

// nfs4OnLock implements LOCK (RFC 7530 section 16.10). A DENIED result
// carries the conflicting lock.
func nfs4OnLock(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4LockArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if req.Reclaim {
		// No grace period: this server keeps no lock state across a
		// restart to reclaim.
		return nfs4ErrNoGrace
	}
	r, status := nfs4LockRange(req.Offset, req.Length, req.LockType)
	if status != nfs4OK {
		return status
	}

	sm := c.w.Server.nfs4State()
	var id nfs4StateID
	var denied *nfs4LockDenied
	if req.Locker.NewLockOwner {
		id, denied, status = sm.lock(req.Locker.OpenOwner.LockOwner, current.fullPath(), r)
	} else {
		id, denied, status = sm.lockByStateID(req.Locker.LockOwner.LockStateID, current.fullPath(), r)
	}
	switch status {
	case nfs4OK:
		return nfs4Encode(res, id)
	case nfs4ErrDenied:
		if encoded := nfs4Encode(res, denied); encoded != nfs4OK {
			return encoded
		}
	}
	return status
}
