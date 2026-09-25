package nfs

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NFSv4.0 state (RFC 7530 section 9): the open and lock stateids a client
// holds, and the client leases they live under.
//
// Opens take no share reservations and byte-range locks are advisory, so
// neither is enforced against READ or WRITE, matching the common cloud
// file-gateway model. Which ranges are locked is decided by the server's
// NFSv4Locker, which an embedder can share with other protocol servers; this
// file owns the protocol state around it.
//
// A stateid's "other" field names one state: an open owner's opens of one
// file, or a lock owner's locks on one. Its seqid is that state's
// generation. It starts at 1 and moves on with every operation that changes
// the state: OPEN of the same file by the same owner, OPEN_CONFIRM,
// OPEN_DOWNGRADE and CLOSE for an open; LOCK and LOCKU for a lock.

const (
	nfs4ReadLT  uint32 = 1
	nfs4WriteLT uint32 = 2
	// Blocking variants are treated as their non-blocking forms: the server
	// answers DENIED and the client polls, which RFC 7530 permits.
	nfs4ReadWLT  uint32 = 3
	nfs4WriteWLT uint32 = 4

	// nfs4LengthEOF as a lock length means "to end of file".
	nfs4LengthEOF = math.MaxUint64

	// State whose client has not renewed its lease within this many lease
	// periods is expired lazily. RFC 7530 allows reclaiming after one lease
	// period; being generous costs little and forgives slow clients.
	nfs4LeaseGracePeriods = 3

	// nfs4SweepInterval spaces the scans for expired leases, which run on
	// the way through a state operation.
	nfs4SweepInterval = time.Second

	// nfs4LockClientPrefix marks locker clients that are NFSv4 client IDs.
	nfs4LockClientPrefix = "nfs4/"
)

type nfs4StateKind uint8

const (
	nfs4OpenState nfs4StateKind = iota + 1
	nfs4LockState
)

// nfs4State is the state behind one stateid.
type nfs4State struct {
	kind  nfs4StateKind
	owner nfs4Owner
	path  string
	id    nfs4StateID
}

// nfs4OwnerKey keeps open owners and lock owners apart: a client may use the
// same bytes for both.
type nfs4OwnerKey struct {
	kind  nfs4StateKind
	owner nfs4Owner
}

// lockerOwner maps an NFSv4 lock owner onto the locker's holder identity.
func (o nfs4Owner) lockerOwner() LockOwner {
	return LockOwner{Client: nfs4LockClient(o.ClientID), Owner: o.Owner}
}

func nfs4LockClient(clientID uint64) string {
	return nfs4LockClientPrefix + strconv.FormatUint(clientID, 16)
}

// nfs4DeniedOwner renders a conflicting holder for LOCK4denied. NFSv4
// holders round-trip their client ID; holders from other protocols report
// client 0 and an owner naming the protocol client.
func nfs4DeniedOwner(o LockOwner) nfs4Owner {
	if strings.HasPrefix(o.Client, nfs4LockClientPrefix) {
		if id, err := strconv.ParseUint(strings.TrimPrefix(o.Client, nfs4LockClientPrefix), 16, 64); err == nil {
			return nfs4Owner{ClientID: id, Owner: o.Owner}
		}
	}
	return nfs4Owner{Owner: o.Client + "/" + o.Owner}
}

// nfs4LockDenied is a LOCK4denied: the lock that conflicts with a request.
type nfs4LockDenied struct {
	Offset   uint64
	Length   uint64
	LockType uint32
	Owner    nfs4Owner
}

func nfs4DeniedFrom(c *LockConflict) *nfs4LockDenied {
	length := uint64(nfs4LengthEOF)
	if c.End != nfs4LengthEOF {
		length = c.End - c.Start
	}
	lockType := nfs4ReadLT
	if c.Exclusive {
		lockType = nfs4WriteLT
	}
	return &nfs4LockDenied{Offset: c.Start, Length: length, LockType: lockType, Owner: nfs4DeniedOwner(c.Owner)}
}

// nfs4LockRange validates a LOCK, LOCKT or LOCKU range and type per RFC 7530
// and returns it as a locker range.
func nfs4LockRange(offset, length uint64, lockType uint32) (LockRange, nfs4Status) {
	if length == 0 {
		return LockRange{}, nfs4ErrInval
	}
	end := uint64(nfs4LengthEOF)
	if length != nfs4LengthEOF {
		if offset > math.MaxUint64-length {
			return LockRange{}, nfs4ErrInval
		}
		end = offset + length
	}
	switch lockType {
	case nfs4ReadLT, nfs4ReadWLT:
		return LockRange{Start: offset, End: end}, nfs4OK
	case nfs4WriteLT, nfs4WriteWLT:
		return LockRange{Start: offset, End: end, Exclusive: true}, nfs4OK
	default:
		return LockRange{}, nfs4ErrInval
	}
}

// nfs4LockerStatus maps a locker error to an NFSv4 status.
func nfs4LockerStatus(err error) nfs4Status {
	if errors.Is(err, ErrLockLimit) {
		return nfs4ErrResource
	}
	return nfs4ErrIO
}

type nfs4StateManager struct {
	mu sync.Mutex
	// locker holds the locked ranges; nil disables locking.
	locker ByteRangeLocker
	// states indexes every stateid by its "other" field.
	states map[[nfs4OtherSize]byte]*nfs4State
	// byOwner finds an owner's state for a file.
	byOwner map[nfs4OwnerKey]map[string]*nfs4State
	// clientSeen tracks lease renewal for lazy expiry.
	clientSeen map[uint64]time.Time
	lastSweep  time.Time
	// counter feeds unique stateid "other" values.
	counter uint64

	now func() time.Time
}

func newNFS4StateManager(locker ByteRangeLocker) *nfs4StateManager {
	return &nfs4StateManager{
		locker:     locker,
		states:     make(map[[nfs4OtherSize]byte]*nfs4State),
		byOwner:    make(map[nfs4OwnerKey]map[string]*nfs4State),
		clientSeen: make(map[uint64]time.Time),
		now:        time.Now,
	}
}

// nfs4State returns the server's NFSv4 state, creating it on first use
// around the server's NFSv4Locker, or an in-memory table when it has none.
func (s *Server) nfs4State() *nfs4StateManager {
	s.nfs4StateOnce.Do(func() {
		locker := s.NFSv4Locker
		if locker == nil {
			locker = NewMemoryLocker(0, 0)
		}
		s.nfs4StateMgr = newNFS4StateManager(locker)
	})
	return s.nfs4StateMgr
}

// touchClientLocked records lease activity and lazily expires state from
// clients that stopped renewing.
func (sm *nfs4StateManager) touchClientLocked(clientID uint64) {
	now := sm.now()
	sm.clientSeen[clientID] = now
	if now.Sub(sm.lastSweep) < nfs4SweepInterval {
		return
	}
	sm.lastSweep = now

	deadline := now.Add(-nfs4LeaseGracePeriods * nfs4LeaseTimeSecs * time.Second)
	for client, seen := range sm.clientSeen {
		if client != clientID && seen.Before(deadline) {
			sm.expireClientLocked(client)
		}
	}
}

// renewClient marks lease renewal (RENEW) without creating state for
// unknown clients.
func (sm *nfs4StateManager) renewClient(clientID uint64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, known := sm.clientSeen[clientID]; known {
		sm.touchClientLocked(clientID)
	}
}

// renewState renews the lease of the client holding a stateid, as any
// operation presenting one does (RFC 7530 section 9.5). Special and unknown
// stateids renew nothing.
func (sm *nfs4StateManager) renewState(id nfs4StateID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if st, ok := sm.states[id.Other]; ok {
		sm.touchClientLocked(st.owner.ClientID)
	}
}

func (sm *nfs4StateManager) expireClientLocked(clientID uint64) {
	for _, st := range sm.states {
		if st.owner.ClientID == clientID {
			sm.dropStateLocked(st)
		}
	}
	delete(sm.clientSeen, clientID)
	if sm.locker != nil {
		sm.locker.ReleaseClient(nfs4LockClient(clientID))
	}
}

func (sm *nfs4StateManager) dropStateLocked(st *nfs4State) {
	delete(sm.states, st.id.Other)
	key := nfs4OwnerKey{st.kind, st.owner}
	if files, ok := sm.byOwner[key]; ok {
		delete(files, st.path)
		if len(files) == 0 {
			delete(sm.byOwner, key)
		}
	}
}

// ownerStateLocked returns owner's state of the given kind for path,
// creating it if there is none.
func (sm *nfs4StateManager) ownerStateLocked(kind nfs4StateKind, owner nfs4Owner, path string) *nfs4State {
	key := nfs4OwnerKey{kind, owner}
	if st := sm.byOwner[key][path]; st != nil {
		return st
	}
	st := &nfs4State{kind: kind, owner: owner, path: path}
	sm.counter++
	marker := "OPEN"
	if kind == nfs4LockState {
		marker = "LOCK"
	}
	copy(st.id.Other[0:4], marker)
	binary.BigEndian.PutUint64(st.id.Other[4:12], sm.counter)
	sm.states[st.id.Other] = st
	if sm.byOwner[key] == nil {
		sm.byOwner[key] = make(map[string]*nfs4State)
	}
	sm.byOwner[key][path] = st
	return st
}

// findLocked returns the state a presented stateid names, if it is of the
// given kind.
func (sm *nfs4StateManager) findLocked(kind nfs4StateKind, id nfs4StateID) (*nfs4State, nfs4Status) {
	st, ok := sm.states[id.Other]
	if !ok || st.kind != kind {
		return nil, nfs4ErrBadStateID
	}
	return st, nfs4OK
}

// findLockLocked returns the lock state a presented stateid names, if it is
// for path: the locker knows the ranges by path.
func (sm *nfs4StateManager) findLockLocked(id nfs4StateID, path string) (*nfs4State, nfs4Status) {
	st, status := sm.findLocked(nfs4LockState, id)
	if status == nfs4OK && st.path != path {
		return nil, nfs4ErrBadStateID
	}
	return st, status
}

// advance moves a state to its next generation and returns its stateid.
func (st *nfs4State) advance() nfs4StateID {
	st.id.Seqid++
	return st.id
}

// open records an OPEN of path by owner.
func (sm *nfs4StateManager) open(owner nfs4Owner, path string) nfs4StateID {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.touchClientLocked(owner.ClientID)
	return sm.ownerStateLocked(nfs4OpenState, owner, path).advance()
}

// updateOpen advances an open state for OPEN_CONFIRM or OPEN_DOWNGRADE.
// The file may have been renamed since it was opened, so the stateid alone
// finds the open.
func (sm *nfs4StateManager) updateOpen(id nfs4StateID) (nfs4StateID, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st, status := sm.findLocked(nfs4OpenState, id)
	if status != nfs4OK {
		return nfs4StateID{}, status
	}
	sm.touchClientLocked(st.owner.ClientID)
	return st.advance(), nfs4OK
}

// close ends an open state (CLOSE).
func (sm *nfs4StateManager) close(id nfs4StateID) (nfs4StateID, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st, status := sm.findLocked(nfs4OpenState, id)
	if status != nfs4OK {
		return nfs4StateID{}, status
	}
	sm.touchClientLocked(st.owner.ClientID)
	next := st.advance()
	sm.dropStateLocked(st)
	return next, nfs4OK
}

// lock acquires or upgrades a range for (owner, path). On conflict it
// returns the conflicting lock.
func (sm *nfs4StateManager) lock(owner nfs4Owner, path string, r LockRange) (nfs4StateID, *nfs4LockDenied, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lockLocked(owner, path, r)
}

// lockByStateID acquires a range using an existing lock stateid.
func (sm *nfs4StateManager) lockByStateID(id nfs4StateID, path string, r LockRange) (nfs4StateID, *nfs4LockDenied, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st, status := sm.findLockLocked(id, path)
	if status != nfs4OK {
		return nfs4StateID{}, nil, status
	}
	return sm.lockLocked(st.owner, path, r)
}

func (sm *nfs4StateManager) lockLocked(owner nfs4Owner, path string, r LockRange) (nfs4StateID, *nfs4LockDenied, nfs4Status) {
	if sm.locker == nil {
		return nfs4StateID{}, nil, nfs4ErrNotSupp
	}
	sm.touchClientLocked(owner.ClientID)

	Log.Debugf("nfs4 LOCK owner={client:%x owner:%x} path=%q range=[%d,%d) exclusive=%t",
		owner.ClientID, owner.Owner, path, r.Start, r.End, r.Exclusive)

	conflict, err := sm.locker.Lock(owner.lockerOwner(), path, r)
	if err != nil {
		return nfs4StateID{}, nil, nfs4LockerStatus(err)
	}
	if conflict != nil {
		return nfs4StateID{}, nfs4DeniedFrom(conflict), nfs4ErrDenied
	}
	return sm.ownerStateLocked(nfs4LockState, owner, path).advance(), nil, nfs4OK
}

// unlock releases a range held under a lock stateid.
func (sm *nfs4StateManager) unlock(id nfs4StateID, path string, r LockRange) (nfs4StateID, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.locker == nil {
		return nfs4StateID{}, nfs4ErrNotSupp
	}
	st, status := sm.findLockLocked(id, path)
	if status != nfs4OK {
		return nfs4StateID{}, status
	}
	sm.touchClientLocked(st.owner.ClientID)

	if err := sm.locker.Unlock(st.owner.lockerOwner(), path, r); err != nil {
		return nfs4StateID{}, nfs4LockerStatus(err)
	}
	return st.advance(), nfs4OK
}

// test checks whether a range could be locked by owner (LOCKT).
func (sm *nfs4StateManager) test(owner nfs4Owner, path string, r LockRange) (*nfs4LockDenied, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.locker == nil {
		return nil, nfs4ErrNotSupp
	}
	sm.touchClientLocked(owner.ClientID)

	conflict, err := sm.locker.Test(owner.lockerOwner(), path, r)
	if err != nil {
		return nil, nfs4LockerStatus(err)
	}
	if conflict != nil {
		return nfs4DeniedFrom(conflict), nfs4ErrDenied
	}
	return nil, nfs4OK
}

// releaseOwner drops all lock state for a lock owner (RELEASE_LOCKOWNER).
func (sm *nfs4StateManager) releaseOwner(owner nfs4Owner) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.touchClientLocked(owner.ClientID)

	for _, st := range sm.byOwner[nfs4OwnerKey{nfs4LockState, owner}] {
		sm.dropStateLocked(st)
	}
	if sm.locker != nil {
		sm.locker.ReleaseOwner(owner.lockerOwner())
	}
}
