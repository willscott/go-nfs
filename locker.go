package nfs

import "errors"

// ByteRangeLocker is the byte-range lock table behind NFSv4 LOCK, LOCKT and
// LOCKU. NFSv4 protocol state (lock stateids, seqids, client leases) stays in
// this package; the locker only decides which ranges are held. Embedders that
// serve the same files over other protocols supply a shared table, so locks
// conflict across protocols.
type ByteRangeLocker interface {
	// Lock grants r to owner on path, replacing the owner's own overlapping
	// ranges. If another owner holds a conflicting range it returns that
	// lock and grants nothing. ErrLockLimit means a state cap was reached.
	Lock(owner LockOwner, path string, r LockRange) (*LockConflict, error)
	// Unlock releases r from owner's ranges on path.
	Unlock(owner LockOwner, path string, r LockRange) error
	// Test returns the lock another owner holds that conflicts with r, or nil.
	Test(owner LockOwner, path string, r LockRange) (*LockConflict, error)
	// ReleaseOwner drops every range owner holds.
	ReleaseOwner(owner LockOwner)
	// ReleaseClient drops every range held by any owner of client.
	ReleaseClient(client string)
}

// LockOwner identifies a lock holder: Client is the protocol client (NFSv4
// holders use "nfs4/<client id in hex>") and Owner the holder within it.
type LockOwner struct {
	Client string
	Owner  string
}

// LockRange is the byte range [Start, End); End == math.MaxUint64 extends to
// end of file. Exclusive is a write lock; otherwise a read lock.
type LockRange struct {
	Start, End uint64
	Exclusive  bool
}

// LockConflict describes a held lock that blocks a request.
type LockConflict struct {
	LockRange
	Owner LockOwner
}

// ErrLockLimit is returned by a ByteRangeLocker that refuses a lock to bound
// its state; LOCK answers NFS4ERR_RESOURCE.
var ErrLockLimit = errors.New("nfs: lock limit exceeded")
