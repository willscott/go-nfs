package nfs

import (
	"bytes"
	"testing"
	"time"
)

// fakeLocker records calls and returns scripted results, so these tests
// cover the NFSv4 protocol layer; range semantics belong to the locker.
type fakeLocker struct {
	conflict *LockConflict
	err      error

	locks           []fakeLockCall
	unlocks         []fakeLockCall
	tests           []fakeLockCall
	releasedOwners  []LockOwner
	releasedClients []string
}

type fakeLockCall struct {
	owner LockOwner
	path  string
	r     LockRange
}

func (f *fakeLocker) Lock(owner LockOwner, path string, r LockRange) (*LockConflict, error) {
	f.locks = append(f.locks, fakeLockCall{owner, path, r})
	return f.conflict, f.err
}

func (f *fakeLocker) Unlock(owner LockOwner, path string, r LockRange) error {
	f.unlocks = append(f.unlocks, fakeLockCall{owner, path, r})
	return f.err
}

func (f *fakeLocker) Test(owner LockOwner, path string, r LockRange) (*LockConflict, error) {
	f.tests = append(f.tests, fakeLockCall{owner, path, r})
	return f.conflict, f.err
}

func (f *fakeLocker) ReleaseOwner(owner LockOwner) {
	f.releasedOwners = append(f.releasedOwners, owner)
}

func (f *fakeLocker) ReleaseClient(client string) {
	f.releasedClients = append(f.releasedClients, client)
}

func mustRange(t *testing.T, offset, length uint64, lockType uint32) lockRange {
	t.Helper()
	r, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		t.Fatalf("makeRange(%d,%d,%d) status = %d", offset, length, lockType, status)
	}
	return r
}

func TestMakeRangeValidation(t *testing.T) {
	if _, status := makeRange(0, 0, writeLT); status != nfs4ErrInval {
		t.Errorf("zero length: status = %d, want INVAL", status)
	}
	if _, status := makeRange(10, ^uint64(0)-5, writeLT); status != nfs4ErrInval {
		t.Errorf("overflowing range: status = %d, want INVAL", status)
	}
	if _, status := makeRange(0, 100, 9); status != nfs4ErrInval {
		t.Errorf("bad lock type: status = %d, want INVAL", status)
	}
	r := mustRange(t, 5, nfs4LengthEOF, readWLT)
	if r.end != nfs4LengthEOF || r.lockType != readLT {
		t.Errorf("EOF blocking read = %+v, want end EOF, type READ", r)
	}
}

func TestLockDelegatesToLockerWithNFS4Owner(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	owner := lockOwnerID{clientID: 0xab, owner: "o"}

	if _, _, status := lm.lock(owner, "/f", mustRange(t, 10, 20, writeLT)); status != nfs4OK {
		t.Fatalf("lock: status = %d", status)
	}
	want := fakeLockCall{
		owner: LockOwner{Client: "nfs4/ab", Owner: "o"},
		path:  "/f",
		r:     LockRange{Start: 10, End: 30, Exclusive: true},
	}
	if len(locker.locks) != 1 || locker.locks[0] != want {
		t.Fatalf("locker calls = %+v, want %+v", locker.locks, want)
	}

	if _, status := lm.test(owner, "/f", mustRange(t, 0, nfs4LengthEOF, readLT)); status != nfs4OK {
		t.Fatalf("test: status = %d", status)
	}
	if got := locker.tests[0].r; got != (LockRange{Start: 0, End: nfs4LengthEOF}) {
		t.Fatalf("test range = %+v, want shared [0, EOF)", got)
	}
}

func TestLockDeniedDescribesConflictingHolder(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	requester := lockOwnerID{clientID: 2, owner: "bob"}

	// Held by another NFSv4 client: its client ID round-trips.
	locker.conflict = &LockConflict{
		LockRange: LockRange{Start: 0, End: 100, Exclusive: true},
		Owner:     LockOwner{Client: "nfs4/1", Owner: "alice"},
	}
	st, denied, status := lm.lock(requester, "/f", mustRange(t, 50, 10, readLT))
	if status != nfs4ErrDenied || st != nil {
		t.Fatalf("lock over conflict: status = %d, state = %v; want DENIED, no state", status, st)
	}
	want := lockDenied{offset: 0, length: 100, lockType: writeLT, owner: lockOwnerID{clientID: 1, owner: "alice"}}
	if *denied != want {
		t.Fatalf("denied = %+v, want %+v", *denied, want)
	}
	if len(lm.states) != 0 {
		t.Fatal("a denied lock must not create lock state")
	}

	// Held by another protocol's client, to end of file, shared.
	locker.conflict = &LockConflict{
		LockRange: LockRange{Start: 5, End: nfs4LengthEOF},
		Owner:     LockOwner{Client: "smb/session-9", Owner: "handle-3"},
	}
	denied, status = lm.test(requester, "/f", mustRange(t, 0, 10, writeLT))
	if status != nfs4ErrDenied {
		t.Fatalf("test over foreign conflict: status = %d, want DENIED", status)
	}
	want = lockDenied{offset: 5, length: nfs4LengthEOF, lockType: readLT, owner: lockOwnerID{owner: "smb/session-9/handle-3"}}
	if *denied != want {
		t.Fatalf("denied = %+v, want %+v", *denied, want)
	}
}

func TestLockStateReuseAndSeqid(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	owner := lockOwnerID{clientID: 1, owner: "o"}

	st, _, status := lm.lock(owner, "/f", mustRange(t, 0, 10, writeLT))
	if status != nfs4OK {
		t.Fatalf("lock: status = %d", status)
	}
	seqBefore := st.seqid

	st2, _, status := lm.lockByStateID(st.other, "/f", mustRange(t, 20, 10, writeLT))
	if status != nfs4OK || st2 != st {
		t.Fatalf("lockByStateID: status = %d, same state = %v", status, st2 == st)
	}
	if st.seqid != seqBefore+1 {
		t.Fatalf("stateid seqid = %d, want %d (must advance)", st.seqid, seqBefore+1)
	}
	if got := locker.locks[1].owner; got != owner.lockerOwner() {
		t.Fatalf("lockByStateID owner = %+v, want the stateid's owner", got)
	}

	if _, status := lm.unlock(st.other, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4OK {
		t.Fatalf("unlock: status = %d", status)
	}
	if st.seqid != seqBefore+2 {
		t.Fatalf("seqid after unlock = %d, want %d", st.seqid, seqBefore+2)
	}
	if len(locker.unlocks) != 1 || locker.unlocks[0].path != "/f" {
		t.Fatalf("locker unlock calls = %+v", locker.unlocks)
	}

	if _, _, status := lm.lock(owner, "/g", mustRange(t, 0, 10, writeLT)); status != nfs4OK {
		t.Fatalf("lock /g: status = %d", status)
	}
	if len(lm.states) != 2 {
		t.Fatalf("states = %d, want one per (owner, file)", len(lm.states))
	}
}

func TestUnlockBadStateID(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	var bogus [nfs4OtherSize]byte
	if _, status := lm.unlock(bogus, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with unknown stateid: status = %d, want BAD_STATEID", status)
	}

	st, _, _ := lm.lock(lockOwnerID{clientID: 1, owner: "o"}, "/f", mustRange(t, 0, 10, writeLT))
	if _, status := lm.unlock(st.other, "/WRONG", mustRange(t, 0, 10, writeLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with wrong path: status = %d, want BAD_STATEID", status)
	}
	if _, _, status := lm.lockByStateID(st.other, "/WRONG", mustRange(t, 0, 10, writeLT)); status != nfs4ErrBadStateID {
		t.Fatalf("lockByStateID with wrong path: status = %d, want BAD_STATEID", status)
	}
	if len(locker.unlocks) != 0 {
		t.Fatal("bad stateids must not reach the locker")
	}
}

func TestLockLimitMapsToResource(t *testing.T) {
	locker := &fakeLocker{err: ErrLockLimit}
	lm := newNFS4LockManager(locker)

	if _, _, status := lm.lock(lockOwnerID{clientID: 1, owner: "o"}, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4ErrResource {
		t.Fatalf("lock over limit: status = %d, want RESOURCE", status)
	}
	if len(lm.states) != 0 {
		t.Fatal("a refused lock must not create lock state")
	}
}

func TestNilLockerDisablesLocking(t *testing.T) {
	lm := newNFS4LockManager(nil)
	owner := lockOwnerID{clientID: 1, owner: "o"}

	if _, _, status := lm.lock(owner, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4ErrNotSupp {
		t.Fatalf("lock: status = %d, want NOTSUPP", status)
	}
	if _, status := lm.test(owner, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4ErrNotSupp {
		t.Fatalf("test: status = %d, want NOTSUPP", status)
	}
	var other [nfs4OtherSize]byte
	if _, status := lm.unlock(other, "/f", mustRange(t, 0, 10, writeLT)); status != nfs4ErrNotSupp {
		t.Fatalf("unlock: status = %d, want NOTSUPP", status)
	}
	lm.releaseOwner(owner)
	lm.renewClient(owner.clientID)
}

func TestReleaseOwnerDropsStateAndRanges(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	owner := lockOwnerID{clientID: 1, owner: "o"}
	other := lockOwnerID{clientID: 1, owner: "p"}
	st, _, _ := lm.lock(owner, "/f", mustRange(t, 0, 100, writeLT))
	lm.lock(owner, "/g", mustRange(t, 0, 100, writeLT))
	lm.lock(other, "/f", mustRange(t, 200, 100, writeLT))

	lm.releaseOwner(owner)

	if len(lm.states) != 1 {
		t.Fatalf("states = %d, want only the other owner's", len(lm.states))
	}
	if _, status := lm.unlock(st.other, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4ErrBadStateID {
		t.Fatalf("released stateid: status = %d, want BAD_STATEID", status)
	}
	if len(locker.releasedOwners) != 1 || locker.releasedOwners[0] != owner.lockerOwner() {
		t.Fatalf("locker released owners = %+v, want %+v", locker.releasedOwners, owner.lockerOwner())
	}
}

func TestLeaseExpiryReleasesClientLocks(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	current := time.Unix(1000, 0)
	lm.now = func() time.Time { return current }

	dead := lockOwnerID{clientID: 1, owner: "dead"}
	deadState, _, status := lm.lock(dead, "/f", mustRange(t, 0, 100, writeLT))
	if status != nfs4OK {
		t.Fatal("setup lock failed")
	}

	// A live client's activity after the grace window expires the dead one.
	current = current.Add(lockLeaseGracePeriods*nfs4LeaseTimeSecs*time.Second + time.Second)
	live := lockOwnerID{clientID: 2, owner: "live"}
	if _, _, status := lm.lock(live, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4OK {
		t.Fatalf("live lock after dead lease expiry: status = %d, want OK", status)
	}
	if _, seen := lm.clientSeen[dead.clientID]; seen {
		t.Fatal("dead client lease record should be gone")
	}
	if len(locker.releasedClients) != 1 || locker.releasedClients[0] != "nfs4/1" {
		t.Fatalf("locker released clients = %v, want [nfs4/1]", locker.releasedClients)
	}
	if _, ok := lm.states[deadState.other]; ok {
		t.Fatal("dead client's lock state should be dropped")
	}
}

func TestRenewKeepsLeaseAlive(t *testing.T) {
	locker := &fakeLocker{}
	lm := newNFS4LockManager(locker)
	current := time.Unix(1000, 0)
	lm.now = func() time.Time { return current }

	holder := lockOwnerID{clientID: 1, owner: "holder"}
	if _, _, status := lm.lock(holder, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4OK {
		t.Fatal("setup lock failed")
	}

	// Renew inside the window repeatedly; nothing may expire even though a
	// second client stays active well past the original grace deadline.
	other := lockOwnerID{clientID: 2, owner: "other"}
	for i := 0; i < 10; i++ {
		current = current.Add(nfs4LeaseTimeSecs * time.Second)
		lm.renewClient(holder.clientID)
		lm.test(other, "/f", mustRange(t, 0, 100, writeLT))
	}
	if len(locker.releasedClients) != 0 {
		t.Fatalf("renewed client was expired: released %v", locker.releasedClients)
	}
}

func TestDeniedOwnerMapping(t *testing.T) {
	if got := deniedOwner(lockOwnerID{clientID: 0xdeadbeef, owner: "o"}.lockerOwner()); got != (lockOwnerID{clientID: 0xdeadbeef, owner: "o"}) {
		t.Fatalf("NFSv4 owner round trip = %+v", got)
	}
	if got := deniedOwner(LockOwner{Client: "nfs4/not-hex", Owner: "o"}); got != (lockOwnerID{owner: "nfs4/not-hex/o"}) {
		t.Fatalf("malformed NFSv4 client = %+v, want descriptive owner", got)
	}
}

func TestCompoundResponseKeepsDeniedLockBody(t *testing.T) {
	// LOCK DENIED results must carry the LOCK4denied payload; clients fail
	// to decode the response without it and retry until the conflict
	// disappears, which reads as a granted lock. (Found via cross-client
	// locking between two real NFS client machines.)
	var deniedBody bytes.Buffer
	wr := newNFS4Writer(&deniedBody)
	writeLockDenied(wr, &lockDenied{offset: 0, length: 100, lockType: writeLT,
		owner: lockOwnerID{clientID: 0xABCD, owner: "conflicting-owner"}})

	results := []nfs4Result{
		{op: opPutFH, status: nfs4OK},
		{op: opLock, status: nfs4ErrDenied, body: deniedBody.Bytes()},
	}

	var out bytes.Buffer
	w := &response{writer: &out, req: &request{xid: 7}}
	if err := writeNFSv4CompoundResponse(w, nfs4ErrDenied, nil, results); err != nil {
		t.Fatalf("writeNFSv4CompoundResponse() error = %v", err)
	}

	if !bytes.Contains(out.Bytes(), deniedBody.Bytes()) {
		t.Fatal("compound response must include the LOCK4denied body for DENIED lock ops")
	}
}

// Made with Bob
