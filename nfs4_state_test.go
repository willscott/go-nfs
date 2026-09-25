package nfs

import (
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

func mustRange(t *testing.T, offset, length uint64, lockType uint32) LockRange {
	t.Helper()
	r, status := nfs4LockRange(offset, length, lockType)
	if status != nfs4OK {
		t.Fatalf("nfs4LockRange(%d,%d,%d) status = %d", offset, length, lockType, status)
	}
	return r
}

func TestLockRangeValidation(t *testing.T) {
	if _, status := nfs4LockRange(0, 0, nfs4WriteLT); status != nfs4ErrInval {
		t.Errorf("zero length: status = %d, want INVAL", status)
	}
	if _, status := nfs4LockRange(10, ^uint64(0)-5, nfs4WriteLT); status != nfs4ErrInval {
		t.Errorf("overflowing range: status = %d, want INVAL", status)
	}
	if _, status := nfs4LockRange(0, 100, 9); status != nfs4ErrInval {
		t.Errorf("bad lock type: status = %d, want INVAL", status)
	}
	if r := mustRange(t, 5, nfs4LengthEOF, nfs4ReadWLT); r != (LockRange{Start: 5, End: nfs4LengthEOF}) {
		t.Errorf("EOF blocking read = %+v, want shared [5, EOF)", r)
	}
	if r := mustRange(t, 5, 10, nfs4WriteWLT); r != (LockRange{Start: 5, End: 15, Exclusive: true}) {
		t.Errorf("blocking write = %+v, want exclusive [5, 15)", r)
	}
}

func TestLockDelegatesToLockerWithNFS4Owner(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	owner := nfs4Owner{ClientID: 0xab, Owner: "o"}

	if _, _, status := sm.lock(owner, "/f", mustRange(t, 10, 20, nfs4WriteLT)); status != nfs4OK {
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

	if _, status := sm.test(owner, "/f", mustRange(t, 0, nfs4LengthEOF, nfs4ReadLT)); status != nfs4OK {
		t.Fatalf("test: status = %d", status)
	}
	if got := locker.tests[0].r; got != (LockRange{Start: 0, End: nfs4LengthEOF}) {
		t.Fatalf("test range = %+v, want shared [0, EOF)", got)
	}
}

func TestLockDeniedDescribesConflictingHolder(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	requester := nfs4Owner{ClientID: 2, Owner: "bob"}

	// Held by another NFSv4 client: its client ID round-trips.
	locker.conflict = &LockConflict{
		LockRange: LockRange{Start: 0, End: 100, Exclusive: true},
		Owner:     LockOwner{Client: "nfs4/1", Owner: "alice"},
	}
	_, denied, status := sm.lock(requester, "/f", mustRange(t, 50, 10, nfs4ReadLT))
	if status != nfs4ErrDenied {
		t.Fatalf("lock over conflict: status = %d, want DENIED", status)
	}
	want := nfs4LockDenied{Offset: 0, Length: 100, LockType: nfs4WriteLT, Owner: nfs4Owner{ClientID: 1, Owner: "alice"}}
	if *denied != want {
		t.Fatalf("denied = %+v, want %+v", *denied, want)
	}
	if len(sm.states) != 0 {
		t.Fatal("a denied lock must not create lock state")
	}

	// Held by another protocol's client, to end of file, shared.
	locker.conflict = &LockConflict{
		LockRange: LockRange{Start: 5, End: nfs4LengthEOF},
		Owner:     LockOwner{Client: "smb/session-9", Owner: "handle-3"},
	}
	denied, status = sm.test(requester, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	if status != nfs4ErrDenied {
		t.Fatalf("test over foreign conflict: status = %d, want DENIED", status)
	}
	want = nfs4LockDenied{Offset: 5, Length: nfs4LengthEOF, LockType: nfs4ReadLT, Owner: nfs4Owner{Owner: "smb/session-9/handle-3"}}
	if *denied != want {
		t.Fatalf("denied = %+v, want %+v", *denied, want)
	}
}

func TestLockStateReuseAndSeqid(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	owner := nfs4Owner{ClientID: 1, Owner: "o"}

	id, _, status := sm.lock(owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	if status != nfs4OK {
		t.Fatalf("lock: status = %d", status)
	}
	if id.Seqid != 1 {
		t.Fatalf("new lock stateid seqid = %d, want 1", id.Seqid)
	}

	id2, _, status := sm.lockByStateID(id, "/f", mustRange(t, 20, 10, nfs4WriteLT))
	if status != nfs4OK || id2.Other != id.Other {
		t.Fatalf("lockByStateID: status = %d, same state = %v", status, id2.Other == id.Other)
	}
	if id2.Seqid != 2 {
		t.Fatalf("stateid seqid = %d, want 2 (must advance)", id2.Seqid)
	}
	if got := locker.locks[1].owner; got != owner.lockerOwner() {
		t.Fatalf("lockByStateID owner = %+v, want the stateid's owner", got)
	}

	id3, status := sm.unlock(id2, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	if status != nfs4OK {
		t.Fatalf("unlock: status = %d", status)
	}
	if id3.Seqid != 3 {
		t.Fatalf("seqid after unlock = %d, want 3", id3.Seqid)
	}
	if len(locker.unlocks) != 1 || locker.unlocks[0].path != "/f" {
		t.Fatalf("locker unlock calls = %+v", locker.unlocks)
	}

	if _, _, status := sm.lock(owner, "/g", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("lock /g: status = %d", status)
	}
	if len(sm.states) != 2 {
		t.Fatalf("states = %d, want one per (owner, file)", len(sm.states))
	}
}

func TestUnlockBadStateID(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	if _, status := sm.unlock(nfs4StateID{}, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with unknown stateid: status = %d, want BAD_STATEID", status)
	}

	id, _, _ := sm.lock(nfs4Owner{ClientID: 1, Owner: "o"}, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	if _, status := sm.unlock(id, "/WRONG", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with wrong path: status = %d, want BAD_STATEID", status)
	}
	if _, _, status := sm.lockByStateID(id, "/WRONG", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("lockByStateID with wrong path: status = %d, want BAD_STATEID", status)
	}

	open := sm.open(nfs4Owner{ClientID: 1, Owner: "o"}, "/f")
	if _, status := sm.unlock(open, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with an open stateid: status = %d, want BAD_STATEID", status)
	}
	if _, status := sm.close(id); status != nfs4ErrBadStateID {
		t.Fatalf("close with a lock stateid: status = %d, want BAD_STATEID", status)
	}
	if len(locker.unlocks) != 0 {
		t.Fatal("bad stateids must not reach the locker")
	}
}

func TestOpenStateGenerations(t *testing.T) {
	sm := newNFS4StateManager(&fakeLocker{})
	owner := nfs4Owner{ClientID: 1, Owner: "o"}

	first := sm.open(owner, "/f")
	if first.Seqid != 1 {
		t.Fatalf("first open seqid = %d, want 1", first.Seqid)
	}
	if again := sm.open(owner, "/f"); again.Other != first.Other || again.Seqid != 2 {
		t.Fatalf("second open = %+v, want the same state at seqid 2", again)
	}
	// The same bytes as a lock owner name a different owner.
	lock, _, _ := sm.lock(owner, "/f", mustRange(t, 0, 1, nfs4WriteLT))
	if lock.Other == first.Other {
		t.Fatal("open and lock state must have different stateids")
	}

	down, status := sm.updateOpen(first)
	if status != nfs4OK || down.Other != first.Other || down.Seqid != 3 {
		t.Fatalf("downgrade = %+v, %d; want seqid 3", down, status)
	}
	closed, status := sm.close(down)
	if status != nfs4OK || closed.Seqid != 4 {
		t.Fatalf("close = %+v, %d; want seqid 4", closed, status)
	}
	if _, status := sm.updateOpen(closed); status != nfs4ErrBadStateID {
		t.Fatalf("downgrade after close: status = %d, want BAD_STATEID", status)
	}
	if reopened := sm.open(owner, "/f"); reopened.Other == first.Other || reopened.Seqid != 1 {
		t.Fatalf("reopen after close = %+v, want a new state at seqid 1", reopened)
	}
}

func TestLockLimitMapsToResource(t *testing.T) {
	locker := &fakeLocker{err: ErrLockLimit}
	sm := newNFS4StateManager(locker)

	if _, _, status := sm.lock(nfs4Owner{ClientID: 1, Owner: "o"}, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrResource {
		t.Fatalf("lock over limit: status = %d, want RESOURCE", status)
	}
	if len(sm.states) != 0 {
		t.Fatal("a refused lock must not create lock state")
	}
}

func TestNilLockerDisablesLocking(t *testing.T) {
	sm := newNFS4StateManager(nil)
	owner := nfs4Owner{ClientID: 1, Owner: "o"}

	if _, _, status := sm.lock(owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("lock: status = %d, want NOTSUPP", status)
	}
	if _, status := sm.test(owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("test: status = %d, want NOTSUPP", status)
	}
	if _, status := sm.unlock(nfs4StateID{}, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("unlock: status = %d, want NOTSUPP", status)
	}
	sm.releaseOwner(owner)
	sm.renewClient(owner.ClientID)
	if id := sm.open(owner, "/f"); id.Seqid != 1 {
		t.Fatalf("open without a locker = %+v, want seqid 1", id)
	}
}

func TestReleaseOwnerDropsStateAndRanges(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	owner := nfs4Owner{ClientID: 1, Owner: "o"}
	other := nfs4Owner{ClientID: 1, Owner: "p"}
	id, _, _ := sm.lock(owner, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	sm.lock(owner, "/g", mustRange(t, 0, 100, nfs4WriteLT))
	sm.lock(other, "/f", mustRange(t, 200, 100, nfs4WriteLT))
	open := sm.open(owner, "/f")

	sm.releaseOwner(owner)

	if len(sm.states) != 2 {
		t.Fatalf("states = %d, want the other owner's lock and the open", len(sm.states))
	}
	if _, status := sm.unlock(id, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("released stateid: status = %d, want BAD_STATEID", status)
	}
	if _, status := sm.close(open); status != nfs4OK {
		t.Fatalf("an open owner with the same bytes must keep its open: status = %d", status)
	}
	if len(locker.releasedOwners) != 1 || locker.releasedOwners[0] != owner.lockerOwner() {
		t.Fatalf("locker released owners = %+v, want %+v", locker.releasedOwners, owner.lockerOwner())
	}
}

func TestLeaseExpiryReleasesClientState(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	current := time.Unix(1000, 0)
	sm.now = func() time.Time { return current }

	dead := nfs4Owner{ClientID: 1, Owner: "dead"}
	deadLock, _, status := sm.lock(dead, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	if status != nfs4OK {
		t.Fatal("setup lock failed")
	}
	deadOpen := sm.open(dead, "/f")

	// A live client's activity after the grace window expires the dead one.
	current = current.Add(nfs4LeaseGracePeriods*nfs4LeaseTimeSecs*time.Second + time.Second)
	live := nfs4Owner{ClientID: 2, Owner: "live"}
	if _, _, status := sm.lock(live, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("live lock after dead lease expiry: status = %d, want OK", status)
	}
	if _, seen := sm.clientSeen[dead.ClientID]; seen {
		t.Fatal("dead client lease record should be gone")
	}
	if len(locker.releasedClients) != 1 || locker.releasedClients[0] != "nfs4/1" {
		t.Fatalf("locker released clients = %v, want [nfs4/1]", locker.releasedClients)
	}
	if _, ok := sm.states[deadLock.Other]; ok {
		t.Fatal("dead client's lock state should be dropped")
	}
	if _, ok := sm.states[deadOpen.Other]; ok {
		t.Fatal("dead client's open state should be dropped")
	}
}

func TestRenewKeepsLeaseAlive(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	current := time.Unix(1000, 0)
	sm.now = func() time.Time { return current }

	holder := nfs4Owner{ClientID: 1, Owner: "holder"}
	if _, _, status := sm.lock(holder, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4OK {
		t.Fatal("setup lock failed")
	}
	open := sm.open(holder, "/f")

	// Renew inside the window repeatedly, by RENEW and by I/O presenting a
	// stateid; nothing may expire even though a second client stays active
	// well past the original grace deadline.
	other := nfs4Owner{ClientID: 2, Owner: "other"}
	for i := 0; i < 10; i++ {
		current = current.Add(nfs4LeaseTimeSecs * time.Second)
		if i%2 == 0 {
			sm.renewClient(holder.ClientID)
		} else {
			sm.renewState(open)
		}
		sm.test(other, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	}
	if len(locker.releasedClients) != 0 {
		t.Fatalf("renewed client was expired: released %v", locker.releasedClients)
	}
}

func TestDeniedOwnerMapping(t *testing.T) {
	if got := nfs4DeniedOwner(nfs4Owner{ClientID: 0xdeadbeef, Owner: "o"}.lockerOwner()); got != (nfs4Owner{ClientID: 0xdeadbeef, Owner: "o"}) {
		t.Fatalf("NFSv4 owner round trip = %+v", got)
	}
	if got := nfs4DeniedOwner(LockOwner{Client: "nfs4/not-hex", Owner: "o"}); got != (nfs4Owner{Owner: "nfs4/not-hex/o"}) {
		t.Fatalf("malformed NFSv4 client = %+v, want descriptive owner", got)
	}
}
