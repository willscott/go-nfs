package nfs

import (
	"crypto/rand"
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

	// A client that has not renewed its lease within this many lease
	// periods is expired, and its state released. RFC 7530 allows expiry
	// after one lease period; being generous costs little and forgives
	// slow clients. The client is told when it next shows up.
	nfs4LeaseGracePeriods = 3
	nfs4LeaseGrace        = nfs4LeaseGracePeriods * nfs4LeaseTimeSecs * time.Second

	// nfs4ExpiredMemory is how long an expired client ID is answered
	// NFS4ERR_EXPIRED; after that it is NFS4ERR_STALE_CLIENTID, which also
	// sends the client to recover.
	nfs4ExpiredMemory = 10 * nfs4LeaseGrace

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

// The special stateids READ, WRITE and SETATTR may present without holding
// state: all zeros for anonymous access, all ones to bypass locks.
var (
	nfs4AnonymousStateID = nfs4StateID{}
	nfs4BypassStateID    = nfs4StateID{
		Seqid: ^uint32(0),
		Other: [nfs4OtherSize]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
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

// nfs4Client is a client ID the server has handed out: its SETCLIENTID
// arguments, the verifier that confirms it, and when its lease was last
// renewed.
type nfs4Client struct {
	id       uint64
	name     string
	verifier [8]byte
	confirm  [8]byte
	renewed  time.Time
}

type nfs4StateManager struct {
	mu sync.Mutex
	// locker holds the locked ranges; nil disables locking.
	locker ByteRangeLocker

	// boot tells this server instance's client IDs and stateids from those
	// a client kept across a restart: it is the high half of every client
	// ID and the first bytes of every stateid's "other" field.
	boot uint32

	// clients are the confirmed client IDs, also indexed by the client's
	// name; unconfirmed are those SETCLIENTID handed out and
	// SETCLIENTID_CONFIRM has not yet confirmed.
	clients     map[uint64]*nfs4Client
	byName      map[string]*nfs4Client
	unconfirmed map[uint64]*nfs4Client
	// expired remembers client IDs whose lease expired, and when, so the
	// client is told NFS4ERR_EXPIRED rather than NFS4ERR_STALE_CLIENTID.
	expired      map[uint64]time.Time
	lastClientID uint32

	// states indexes every stateid by its "other" field.
	states map[[nfs4OtherSize]byte]*nfs4State
	// byOwner finds an owner's state for a file.
	byOwner   map[nfs4OwnerKey]map[string]*nfs4State
	lastSweep time.Time
	// counter feeds unique stateid "other" values.
	counter uint64

	now func() time.Time
}

func newNFS4StateManager(locker ByteRangeLocker) *nfs4StateManager {
	var boot [4]byte
	_, _ = rand.Read(boot[:])
	return &nfs4StateManager{
		locker:      locker,
		boot:        binary.BigEndian.Uint32(boot[:]),
		clients:     make(map[uint64]*nfs4Client),
		byName:      make(map[string]*nfs4Client),
		unconfirmed: make(map[uint64]*nfs4Client),
		expired:     make(map[uint64]time.Time),
		states:      make(map[[nfs4OtherSize]byte]*nfs4State),
		byOwner:     make(map[nfs4OwnerKey]map[string]*nfs4State),
		now:         time.Now,
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

// setClientID hands out a client ID for a client's name and boot verifier
// (SETCLIENTID). A client that presents the verifier it was confirmed with
// gets the same ID back; a new verifier means the client rebooted, and it
// gets a new ID, which replaces the old one and its state once confirmed.
func (sm *nfs4StateManager) setClientID(name []byte, verifier [8]byte) (uint64, [8]byte) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sweepLocked()

	var id uint64
	if c := sm.byName[string(name)]; c != nil && c.verifier == verifier {
		id = c.id
	} else {
		sm.lastClientID++
		id = uint64(sm.boot)<<32 | uint64(sm.lastClientID)
	}
	for uid, u := range sm.unconfirmed {
		if u.name == string(name) {
			delete(sm.unconfirmed, uid)
		}
	}
	c := &nfs4Client{id: id, name: string(name), verifier: verifier, renewed: sm.now()}
	_, _ = rand.Read(c.confirm[:])
	sm.unconfirmed[id] = c
	return id, c.confirm
}

// confirmClientID confirms a client ID (SETCLIENTID_CONFIRM), starting its
// lease. It releases the state of the ID it replaces.
func (sm *nfs4StateManager) confirmClientID(id uint64, confirm [8]byte) nfs4Status {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sweepLocked()

	if u := sm.unconfirmed[id]; u != nil && u.confirm == confirm {
		delete(sm.unconfirmed, id)
		if old := sm.byName[u.name]; old != nil && old.id != id {
			sm.dropClientLocked(old.id)
		}
		u.renewed = sm.now()
		sm.clients[id] = u
		sm.byName[u.name] = u
		return nfs4OK
	}
	if c := sm.clients[id]; c != nil && c.confirm == confirm {
		// A retransmitted confirmation.
		c.renewed = sm.now()
		return nfs4OK
	}
	return nfs4ErrStaleClientID
}

// renewClient renews a client's lease (RENEW).
func (sm *nfs4StateManager) renewClient(clientID uint64) nfs4Status {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.checkClientLocked(clientID)
}

// checkClientLocked validates a client ID a request presents and renews
// its lease. A client whose lease has run out is told NFS4ERR_EXPIRED and
// one this server instance never confirmed NFS4ERR_STALE_CLIENTID, so the
// client knows its opens and locks are gone and recovers.
func (sm *nfs4StateManager) checkClientLocked(clientID uint64) nfs4Status {
	sm.sweepLocked()
	now := sm.now()
	if c := sm.clients[clientID]; c != nil {
		if now.Sub(c.renewed) > nfs4LeaseGrace {
			sm.expireClientLocked(clientID)
			return nfs4ErrExpired
		}
		c.renewed = now
		return nfs4OK
	}
	if _, ok := sm.expired[clientID]; ok {
		return nfs4ErrExpired
	}
	return nfs4ErrStaleClientID
}

// sweepLocked expires clients that stopped renewing their leases, so
// their locks stop blocking others, and forgets unconfirmed client IDs and
// expiries that are old enough not to matter.
func (sm *nfs4StateManager) sweepLocked() {
	now := sm.now()
	if now.Sub(sm.lastSweep) < nfs4SweepInterval {
		return
	}
	sm.lastSweep = now

	for id, c := range sm.clients {
		if now.Sub(c.renewed) > nfs4LeaseGrace {
			sm.expireClientLocked(id)
		}
	}
	for id, c := range sm.unconfirmed {
		if now.Sub(c.renewed) > nfs4LeaseGrace {
			delete(sm.unconfirmed, id)
		}
	}
	for id, when := range sm.expired {
		if now.Sub(when) > nfs4ExpiredMemory {
			delete(sm.expired, id)
		}
	}
}

// expireClientLocked ends a client's lease, releasing its state, and
// remembers that it expired.
func (sm *nfs4StateManager) expireClientLocked(clientID uint64) {
	sm.dropClientLocked(clientID)
	sm.expired[clientID] = sm.now()
}

// dropClientLocked forgets a client ID and releases its state.
func (sm *nfs4StateManager) dropClientLocked(clientID uint64) {
	for _, st := range sm.states {
		if st.owner.ClientID == clientID {
			sm.dropStateLocked(st)
		}
	}
	if c := sm.clients[clientID]; c != nil {
		delete(sm.clients, clientID)
		if sm.byName[c.name] == c {
			delete(sm.byName, c.name)
		}
	}
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
	binary.BigEndian.PutUint32(st.id.Other[0:4], sm.boot)
	binary.BigEndian.PutUint64(st.id.Other[4:12], sm.counter)
	sm.states[st.id.Other] = st
	if sm.byOwner[key] == nil {
		sm.byOwner[key] = make(map[string]*nfs4State)
	}
	sm.byOwner[key][path] = st
	return st
}

// findLocked returns the state a presented stateid names, if it is of the
// given kind, renewing its client's lease. A stateid from before a server
// restart is NFS4ERR_STALE_STATEID, and one of a client whose lease has
// expired NFS4ERR_EXPIRED.
func (sm *nfs4StateManager) findLocked(kind nfs4StateKind, id nfs4StateID) (*nfs4State, nfs4Status) {
	if binary.BigEndian.Uint32(id.Other[0:4]) != sm.boot {
		return nil, nfs4ErrStaleStateID
	}
	st, ok := sm.states[id.Other]
	if !ok || st.kind != kind {
		return nil, nfs4ErrBadStateID
	}
	if status := sm.checkClientLocked(st.owner.ClientID); status != nfs4OK {
		return nil, status
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

// checkIO validates the stateid READ, WRITE or SETATTR presents, renewing
// its client's lease (RFC 7530 section 9.5). The special stateids of all
// zeros and all ones need no state.
func (sm *nfs4StateManager) checkIO(id nfs4StateID) nfs4Status {
	if id == nfs4AnonymousStateID || id == nfs4BypassStateID {
		return nfs4OK
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, status := sm.findLocked(nfs4OpenState, id); status != nfs4ErrBadStateID {
		return status
	}
	_, status := sm.findLocked(nfs4LockState, id)
	return status
}

// advance moves a state to its next generation and returns its stateid.
func (st *nfs4State) advance() nfs4StateID {
	st.id.Seqid++
	return st.id
}

// checkClient validates the client ID an OPEN, LOCKT or RELEASE_LOCKOWNER
// presents, renewing its lease.
func (sm *nfs4StateManager) checkClient(clientID uint64) nfs4Status {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.checkClientLocked(clientID)
}

// open records an OPEN of path by owner.
func (sm *nfs4StateManager) open(owner nfs4Owner, path string) (nfs4StateID, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if status := sm.checkClientLocked(owner.ClientID); status != nfs4OK {
		return nfs4StateID{}, status
	}
	return sm.ownerStateLocked(nfs4OpenState, owner, path).advance(), nfs4OK
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
	next := st.advance()
	sm.dropStateLocked(st)
	return next, nfs4OK
}

// lock acquires or upgrades a range for a lock owner new to the file, under
// the open it presents. On conflict it returns the conflicting lock.
func (sm *nfs4StateManager) lock(open nfs4StateID, owner nfs4Owner, path string, r LockRange) (nfs4StateID, *nfs4LockDenied, nfs4Status) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st, status := sm.findLocked(nfs4OpenState, open)
	if status != nfs4OK {
		return nfs4StateID{}, nil, status
	}
	if st.owner.ClientID != owner.ClientID {
		return nfs4StateID{}, nil, nfs4ErrBadStateID
	}
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
	if status := sm.checkClientLocked(owner.ClientID); status != nfs4OK {
		return nil, status
	}

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
func (sm *nfs4StateManager) releaseOwner(owner nfs4Owner) nfs4Status {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if status := sm.checkClientLocked(owner.ClientID); status != nfs4OK {
		return status
	}

	for _, st := range sm.byOwner[nfs4OwnerKey{nfs4LockState, owner}] {
		sm.dropStateLocked(st)
	}
	if sm.locker != nil {
		sm.locker.ReleaseOwner(owner.lockerOwner())
	}
	return nfs4OK
}
