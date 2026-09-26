package nfs

import (
	"strconv"
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

// nfs4TestClient returns a confirmed client ID for name.
func nfs4TestClient(t *testing.T, sm *nfs4StateManager, name string) uint64 {
	t.Helper()
	id, confirm := sm.setClientID([]byte(name), [8]byte{1})
	if status := sm.confirmClientID(id, confirm); status != nfs4OK {
		t.Fatalf("confirming client %s: status = %d", name, status)
	}
	return id
}

func nfs4TestOpen(t *testing.T, sm *nfs4StateManager, owner nfs4Owner, path string) nfs4StateID {
	t.Helper()
	id, status := sm.open(owner, path)
	if status != nfs4OK {
		t.Fatalf("open %s: status = %d", path, status)
	}
	return id
}

// nfs4TestLock locks as a lock owner new to path, under an open of path by
// an open owner of the same client.
func nfs4TestLock(t *testing.T, sm *nfs4StateManager, owner nfs4Owner, path string, r LockRange) (nfs4StateID, *nfs4LockDenied, nfs4Status) {
	t.Helper()
	open := nfs4TestOpen(t, sm, nfs4Owner{ClientID: owner.ClientID, Owner: "open"}, path)
	return sm.lock(open, owner, path, r)
}

func nfs4LockStates(sm *nfs4StateManager) int {
	n := 0
	for _, st := range sm.states {
		if st.kind == nfs4LockState {
			n++
		}
	}
	return n
}

func TestLockDelegatesToLockerWithNFS4Owner(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}

	if _, _, status := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 10, 20, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("lock: status = %d", status)
	}
	want := fakeLockCall{
		owner: LockOwner{Client: "nfs4/" + strconv.FormatUint(owner.ClientID, 16), Owner: "o"},
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
	requester := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "bob"}

	// Held by another NFSv4 client: its client ID round-trips.
	locker.conflict = &LockConflict{
		LockRange: LockRange{Start: 0, End: 100, Exclusive: true},
		Owner:     LockOwner{Client: "nfs4/1", Owner: "alice"},
	}
	_, denied, status := nfs4TestLock(t, sm, requester, "/f", mustRange(t, 50, 10, nfs4ReadLT))
	if status != nfs4ErrDenied {
		t.Fatalf("lock over conflict: status = %d, want DENIED", status)
	}
	want := nfs4LockDenied{Offset: 0, Length: 100, LockType: nfs4WriteLT, Owner: nfs4Owner{ClientID: 1, Owner: "alice"}}
	if *denied != want {
		t.Fatalf("denied = %+v, want %+v", *denied, want)
	}
	if nfs4LockStates(sm) != 0 {
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
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}

	id, _, status := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))
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

	if _, _, status := nfs4TestLock(t, sm, owner, "/g", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("lock /g: status = %d", status)
	}
	if n := nfs4LockStates(sm); n != 2 {
		t.Fatalf("lock states = %d, want one per (owner, file)", n)
	}
}

func TestUnlockBadStateID(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	if _, status := sm.unlock(nfs4StateID{}, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrStaleStateID {
		t.Fatalf("unlock with another server's stateid: status = %d, want STALE_STATEID", status)
	}

	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}
	id, _, _ := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	unknown := id
	unknown.Other[11]++
	if _, status := sm.unlock(unknown, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with unknown stateid: status = %d, want BAD_STATEID", status)
	}
	if _, status := sm.unlock(id, "/WRONG", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with wrong path: status = %d, want BAD_STATEID", status)
	}
	if _, _, status := sm.lockByStateID(id, "/WRONG", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("lockByStateID with wrong path: status = %d, want BAD_STATEID", status)
	}

	open := nfs4TestOpen(t, sm, owner, "/f")
	if _, status := sm.unlock(open, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with an open stateid: status = %d, want BAD_STATEID", status)
	}
	if _, status := sm.close(id); status != nfs4ErrBadStateID {
		t.Fatalf("close with a lock stateid: status = %d, want BAD_STATEID", status)
	}
	if _, _, status := sm.lock(id, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("new lock owner under a lock stateid: status = %d, want BAD_STATEID", status)
	}
	other := nfs4Owner{ClientID: nfs4TestClient(t, sm, "other"), Owner: "o"}
	if _, _, status := sm.lock(open, other, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrBadStateID {
		t.Fatalf("new lock owner under another client's open: status = %d, want BAD_STATEID", status)
	}
	if len(locker.unlocks) != 0 || len(locker.locks) != 1 {
		t.Fatal("bad stateids must not reach the locker")
	}
}

func TestOpenStateGenerations(t *testing.T) {
	sm := newNFS4StateManager(&fakeLocker{})
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}

	first := nfs4TestOpen(t, sm, owner, "/f")
	if first.Seqid != 1 {
		t.Fatalf("first open seqid = %d, want 1", first.Seqid)
	}
	if again := nfs4TestOpen(t, sm, owner, "/f"); again.Other != first.Other || again.Seqid != 2 {
		t.Fatalf("second open = %+v, want the same state at seqid 2", again)
	}
	// The same bytes as a lock owner name a different owner.
	lock, _, _ := sm.lock(first, owner, "/f", mustRange(t, 0, 1, nfs4WriteLT))
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
	if reopened := nfs4TestOpen(t, sm, owner, "/f"); reopened.Other == first.Other || reopened.Seqid != 1 {
		t.Fatalf("reopen after close = %+v, want a new state at seqid 1", reopened)
	}
}

func TestLockLimitMapsToResource(t *testing.T) {
	locker := &fakeLocker{err: ErrLockLimit}
	sm := newNFS4StateManager(locker)
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}

	if _, _, status := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrResource {
		t.Fatalf("lock over limit: status = %d, want RESOURCE", status)
	}
	if nfs4LockStates(sm) != 0 {
		t.Fatal("a refused lock must not create lock state")
	}
}

func TestNilLockerDisablesLocking(t *testing.T) {
	sm := newNFS4StateManager(nil)
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}

	if _, _, status := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("lock: status = %d, want NOTSUPP", status)
	}
	if _, status := sm.test(owner, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("test: status = %d, want NOTSUPP", status)
	}
	if _, status := sm.unlock(nfs4StateID{}, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrNotSupp {
		t.Fatalf("unlock: status = %d, want NOTSUPP", status)
	}
	if status := sm.releaseOwner(owner); status != nfs4OK {
		t.Fatalf("release owner: status = %d", status)
	}
	if status := sm.renewClient(owner.ClientID); status != nfs4OK {
		t.Fatalf("renew: status = %d", status)
	}
}

func TestReleaseOwnerDropsStateAndRanges(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	client := nfs4TestClient(t, sm, "c")
	owner := nfs4Owner{ClientID: client, Owner: "o"}
	other := nfs4Owner{ClientID: client, Owner: "p"}
	id, _, _ := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	nfs4TestLock(t, sm, owner, "/g", mustRange(t, 0, 100, nfs4WriteLT))
	nfs4TestLock(t, sm, other, "/f", mustRange(t, 200, 100, nfs4WriteLT))
	open := nfs4TestOpen(t, sm, owner, "/f")

	if status := sm.releaseOwner(owner); status != nfs4OK {
		t.Fatalf("release owner: status = %d", status)
	}

	if n := nfs4LockStates(sm); n != 1 {
		t.Fatalf("lock states = %d, want the other owner's", n)
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

func TestLeaseExpiryReleasesClientStateAndTellsTheClient(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	current := time.Unix(1000, 0)
	sm.now = func() time.Time { return current }

	dead := nfs4Owner{ClientID: nfs4TestClient(t, sm, "dead"), Owner: "dead"}
	live := nfs4Owner{ClientID: nfs4TestClient(t, sm, "live"), Owner: "live"}
	deadLock, _, status := nfs4TestLock(t, sm, dead, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	if status != nfs4OK {
		t.Fatal("setup lock failed")
	}
	deadOpen := nfs4TestOpen(t, sm, dead, "/f")

	// A live client's activity after the grace window expires the dead one.
	current = current.Add(nfs4LeaseGrace / 2)
	if status := sm.renewClient(live.ClientID); status != nfs4OK {
		t.Fatalf("live RENEW: status = %d", status)
	}
	current = current.Add(nfs4LeaseGrace/2 + time.Second)
	if _, status := sm.test(live, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("live lock test after dead lease expiry: status = %d, want OK", status)
	}
	if len(locker.releasedClients) != 1 || locker.releasedClients[0] != nfs4LockClient(dead.ClientID) {
		t.Fatalf("locker released clients = %v, want [%s]", locker.releasedClients, nfs4LockClient(dead.ClientID))
	}
	if _, ok := sm.states[deadLock.Other]; ok {
		t.Fatal("dead client's lock state should be dropped")
	}
	if _, ok := sm.states[deadOpen.Other]; ok {
		t.Fatal("dead client's open state should be dropped")
	}

	// When the dead client comes back, it learns its lease expired.
	if status := sm.renewClient(dead.ClientID); status != nfs4ErrExpired {
		t.Fatalf("RENEW of an expired client: status = %d, want EXPIRED", status)
	}
	if _, status := sm.open(dead, "/f"); status != nfs4ErrExpired {
		t.Fatalf("OPEN by an expired client: status = %d, want EXPIRED", status)
	}
	if _, status := sm.test(dead, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4ErrExpired {
		t.Fatalf("LOCKT by an expired client: status = %d, want EXPIRED", status)
	}

	// Having recovered, it has a new lease.
	again := nfs4TestClient(t, sm, "dead")
	if again == dead.ClientID {
		t.Fatal("recovered client got its expired client ID back")
	}
	if status := sm.renewClient(again); status != nfs4OK {
		t.Fatalf("RENEW after recovery: status = %d", status)
	}
}

func TestIdleClientIsExpiredWhenItReturns(t *testing.T) {
	sm := newNFS4StateManager(&fakeLocker{})
	current := time.Unix(1000, 0)
	sm.now = func() time.Time { return current }
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}
	open := nfs4TestOpen(t, sm, owner, "/f")

	current = current.Add(nfs4LeaseGrace + time.Second)
	if status := sm.checkIO(open); status != nfs4ErrExpired {
		t.Fatalf("READ after the lease ran out: status = %d, want EXPIRED", status)
	}
}

func TestRenewKeepsLeaseAlive(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)
	current := time.Unix(1000, 0)
	sm.now = func() time.Time { return current }

	holder := nfs4Owner{ClientID: nfs4TestClient(t, sm, "holder"), Owner: "holder"}
	if _, _, status := nfs4TestLock(t, sm, holder, "/f", mustRange(t, 0, 100, nfs4WriteLT)); status != nfs4OK {
		t.Fatal("setup lock failed")
	}
	open := nfs4TestOpen(t, sm, holder, "/f")

	// Renew inside the window repeatedly, by RENEW and by I/O presenting a
	// stateid; nothing may expire even though a second client stays active
	// well past the original grace deadline.
	other := nfs4Owner{ClientID: nfs4TestClient(t, sm, "other"), Owner: "other"}
	for i := 0; i < 10; i++ {
		current = current.Add(nfs4LeaseTimeSecs * time.Second)
		var status nfs4Status
		if i%2 == 0 {
			status = sm.renewClient(holder.ClientID)
		} else {
			status = sm.checkIO(open)
		}
		if status != nfs4OK {
			t.Fatalf("renewal %d: status = %d", i, status)
		}
		sm.test(other, "/f", mustRange(t, 0, 100, nfs4WriteLT))
	}
	if len(locker.releasedClients) != 0 {
		t.Fatalf("renewed client was expired: released %v", locker.releasedClients)
	}
}

func TestStateFromBeforeRestartIsStale(t *testing.T) {
	before := newNFS4StateManager(&fakeLocker{})
	owner := nfs4Owner{ClientID: nfs4TestClient(t, before, "c"), Owner: "o"}
	open := nfs4TestOpen(t, before, owner, "/f")
	lock, _, _ := before.lock(open, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))

	after := newNFS4StateManager(&fakeLocker{})
	if after.boot == before.boot {
		after.boot++
	}
	nfs4TestClient(t, after, "someone else")
	nfs4TestOpen(t, after, nfs4Owner{ClientID: nfs4TestClient(t, after, "x"), Owner: "o"}, "/g")

	if status := after.renewClient(owner.ClientID); status != nfs4ErrStaleClientID {
		t.Fatalf("RENEW with a client ID from before a restart: status = %d, want STALE_CLIENTID", status)
	}
	if status := after.checkIO(open); status != nfs4ErrStaleStateID {
		t.Fatalf("READ with an open stateid from before a restart: status = %d, want STALE_STATEID", status)
	}
	if _, status := after.close(open); status != nfs4ErrStaleStateID {
		t.Fatalf("CLOSE with a stateid from before a restart: status = %d, want STALE_STATEID", status)
	}
	if _, status := after.unlock(lock, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4ErrStaleStateID {
		t.Fatalf("LOCKU with a stateid from before a restart: status = %d, want STALE_STATEID", status)
	}
}

func TestSetClientID(t *testing.T) {
	locker := &fakeLocker{}
	sm := newNFS4StateManager(locker)

	id, confirm := sm.setClientID([]byte("c"), [8]byte{1})
	if status := sm.renewClient(id); status != nfs4ErrStaleClientID {
		t.Fatalf("RENEW before confirmation: status = %d, want STALE_CLIENTID", status)
	}
	if status := sm.confirmClientID(id, [8]byte{9}); status != nfs4ErrStaleClientID {
		t.Fatalf("confirm with the wrong verifier: status = %d, want STALE_CLIENTID", status)
	}
	if status := sm.confirmClientID(id, confirm); status != nfs4OK {
		t.Fatalf("confirm: status = %d", status)
	}
	if status := sm.confirmClientID(id, confirm); status != nfs4OK {
		t.Fatalf("retransmitted confirm: status = %d", status)
	}
	owner := nfs4Owner{ClientID: id, Owner: "o"}
	lock, _, _ := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))

	// The same boot verifier keeps the client ID and its state.
	if same, confirm := sm.setClientID([]byte("c"), [8]byte{1}); same != id {
		t.Fatalf("SETCLIENTID with the same verifier = %x, want %x", same, id)
	} else if status := sm.confirmClientID(same, confirm); status != nfs4OK {
		t.Fatalf("confirm: status = %d", status)
	}
	if _, status := sm.unlock(lock, "/f", mustRange(t, 0, 10, nfs4WriteLT)); status != nfs4OK {
		t.Fatalf("lock state after SETCLIENTID with the same verifier: status = %d", status)
	}

	// A new boot verifier means the client rebooted: once the new ID is
	// confirmed, the old one and its state are gone.
	rebooted, confirm := sm.setClientID([]byte("c"), [8]byte{2})
	if rebooted == id {
		t.Fatal("rebooted client got its old client ID")
	}
	if status := sm.renewClient(id); status != nfs4OK {
		t.Fatalf("old client ID before the new one is confirmed: status = %d", status)
	}
	if status := sm.confirmClientID(rebooted, confirm); status != nfs4OK {
		t.Fatalf("confirm: status = %d", status)
	}
	if status := sm.renewClient(id); status != nfs4ErrStaleClientID {
		t.Fatalf("replaced client ID: status = %d, want STALE_CLIENTID", status)
	}
	if len(sm.states) != 0 || len(locker.releasedClients) != 1 {
		t.Fatalf("replaced client's state not released: %d states, released %v", len(sm.states), locker.releasedClients)
	}
}

func TestCheckIOSpecialStateIDs(t *testing.T) {
	sm := newNFS4StateManager(&fakeLocker{})
	if status := sm.checkIO(nfs4AnonymousStateID); status != nfs4OK {
		t.Fatalf("anonymous stateid: status = %d", status)
	}
	if status := sm.checkIO(nfs4BypassStateID); status != nfs4OK {
		t.Fatalf("bypass stateid: status = %d", status)
	}
	owner := nfs4Owner{ClientID: nfs4TestClient(t, sm, "c"), Owner: "o"}
	lock, _, _ := nfs4TestLock(t, sm, owner, "/f", mustRange(t, 0, 10, nfs4WriteLT))
	if status := sm.checkIO(lock); status != nfs4OK {
		t.Fatalf("lock stateid: status = %d", status)
	}
	lock.Other[11]++
	if status := sm.checkIO(lock); status != nfs4ErrBadStateID {
		t.Fatalf("unknown stateid: status = %d, want BAD_STATEID", status)
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
