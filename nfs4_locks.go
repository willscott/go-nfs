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

// NFSv4.0 advisory byte-range locking (RFC 7530 sections 9 and 16.10-16.12).
//
// Locks are advisory only: they are never enforced against READ or WRITE,
// matching the common cloud file-gateway model. Which ranges are held is
// decided by the server's ByteRangeLocker, which an embedder can share with
// other protocol servers. This file owns the NFSv4 protocol state around it.
//
// Lock state uses real bookkeeping (owners, stateids with incrementing
// seqids, client leases) even though the rest of this v4.0 implementation is
// intentionally stateless: the Linux client round-trips lock stateids and
// expects POSIX range semantics (same-owner overlap replaces, different-owner
// conflicts are denied with the conflicting lock described).

const (
	readLT  uint32 = 1
	writeLT uint32 = 2
	// Blocking variants are treated as their non-blocking forms: the server
	// answers DENIED and the client polls, which RFC 7530 permits.
	readWLT  uint32 = 3
	writeWLT uint32 = 4

	// nfs4LengthEOF as a lock length means "to end of file".
	nfs4LengthEOF = math.MaxUint64

	// Lock state whose client has not renewed within this many lease
	// periods is expired lazily. RFC allows reclaiming after one lease
	// period; being generous costs little and forgives slow clients.
	lockLeaseGracePeriods = 3

	// nfs4LockClientPrefix marks locker clients that are NFSv4 client IDs.
	nfs4LockClientPrefix = "nfs4/"
)

// lockOwnerID identifies a lock owner: the client's short-form id plus the
// client-provided opaque owner bytes (RFC 7530 lock_owner4).
type lockOwnerID struct {
	clientID uint64
	owner    string
}

// lockerOwner maps an NFSv4 lock owner onto the locker's holder identity.
func (o lockOwnerID) lockerOwner() LockOwner {
	return LockOwner{Client: nfs4LockClient(o.clientID), Owner: o.owner}
}

func nfs4LockClient(clientID uint64) string {
	return nfs4LockClientPrefix + strconv.FormatUint(clientID, 16)
}

// deniedOwner renders a conflicting holder for LOCK4denied. NFSv4 holders
// round-trip their client ID; holders from other protocols report client 0
// and an owner naming the protocol client.
func deniedOwner(o LockOwner) lockOwnerID {
	if strings.HasPrefix(o.Client, nfs4LockClientPrefix) {
		if id, err := strconv.ParseUint(strings.TrimPrefix(o.Client, nfs4LockClientPrefix), 16, 64); err == nil {
			return lockOwnerID{clientID: id, owner: o.Owner}
		}
	}
	return lockOwnerID{owner: o.Client + "/" + o.Owner}
}

// lockRange is a requested byte range: [start, end) with end == nfs4LengthEOF
// meaning to end of file. Advisory READ/WRITE type per POSIX.
type lockRange struct {
	start, end uint64
	lockType   uint32
}

func (r lockRange) lockerRange() LockRange {
	return LockRange{Start: r.start, End: r.end, Exclusive: r.lockType == writeLT}
}

// lockState is the per-(owner, file) protocol state behind one lock stateid.
type lockState struct {
	owner lockOwnerID
	path  string
	other [nfs4OtherSize]byte
	seqid uint32
}

type nfs4LockManager struct {
	mu sync.Mutex
	// locker holds the ranges; nil disables locking.
	locker ByteRangeLocker
	// states indexes every lock stateid by its "other" field.
	states map[[nfs4OtherSize]byte]*lockState
	// byOwner finds the existing state for (owner, path) on repeat LOCKs
	// that present the open stateid again.
	byOwner map[lockOwnerID]map[string]*lockState
	// clientSeen tracks lease renewal for lazy expiry.
	clientSeen map[uint64]time.Time
	// counter feeds unique stateid "other" values.
	counter uint64

	now func() time.Time
}

func newNFS4LockManager(locker ByteRangeLocker) *nfs4LockManager {
	return &nfs4LockManager{
		locker:     locker,
		states:     make(map[[nfs4OtherSize]byte]*lockState),
		byOwner:    make(map[lockOwnerID]map[string]*lockState),
		clientSeen: make(map[uint64]time.Time),
		now:        time.Now,
	}
}

// lockDenied describes the conflicting lock for a DENIED response.
type lockDenied struct {
	offset   uint64
	length   uint64
	lockType uint32
	owner    lockOwnerID
}

func conflictToDenied(c *LockConflict) *lockDenied {
	length := uint64(nfs4LengthEOF)
	if c.End != nfs4LengthEOF {
		length = c.End - c.Start
	}
	lockType := readLT
	if c.Exclusive {
		lockType = writeLT
	}
	return &lockDenied{offset: c.Start, length: length, lockType: lockType, owner: deniedOwner(c.Owner)}
}

// makeRange validates RFC 7530 offset/length rules.
func makeRange(offset, length uint64, lockType uint32) (lockRange, nfs4Status) {
	if length == 0 {
		return lockRange{}, nfs4ErrInval
	}
	end := uint64(nfs4LengthEOF)
	if length != nfs4LengthEOF {
		if offset > math.MaxUint64-length {
			return lockRange{}, nfs4ErrInval
		}
		end = offset + length
	}
	normalized := lockType
	if normalized == readWLT {
		normalized = readLT
	}
	if normalized == writeWLT {
		normalized = writeLT
	}
	if normalized != readLT && normalized != writeLT {
		return lockRange{}, nfs4ErrInval
	}
	return lockRange{start: offset, end: end, lockType: normalized}, nfs4OK
}

// lockerStatus maps a locker error to an NFSv4 status.
func lockerStatus(err error) nfs4Status {
	if errors.Is(err, ErrLockLimit) {
		return nfs4ErrResource
	}
	return nfs4ErrIO
}

// touchClient records lease activity and lazily expires state from clients
// that stopped renewing.
func (lm *nfs4LockManager) touchClient(clientID uint64) {
	now := lm.now()
	lm.clientSeen[clientID] = now

	deadline := now.Add(-lockLeaseGracePeriods * nfs4LeaseTimeSecs * time.Second)
	for client, seen := range lm.clientSeen {
		if client == clientID || !seen.Before(deadline) {
			continue
		}
		lm.expireClientLocked(client)
	}
}

// renewClient marks lease renewal without creating state for unknown clients.
func (lm *nfs4LockManager) renewClient(clientID uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if _, known := lm.clientSeen[clientID]; known {
		lm.touchClient(clientID)
	}
}

func (lm *nfs4LockManager) expireClientLocked(clientID uint64) {
	for other, st := range lm.states {
		if st.owner.clientID == clientID {
			lm.dropStateLocked(other, st)
		}
	}
	delete(lm.clientSeen, clientID)
	if lm.locker != nil {
		lm.locker.ReleaseClient(nfs4LockClient(clientID))
	}
}

func (lm *nfs4LockManager) dropStateLocked(other [nfs4OtherSize]byte, st *lockState) {
	delete(lm.states, other)
	if files, ok := lm.byOwner[st.owner]; ok {
		delete(files, st.path)
		if len(files) == 0 {
			delete(lm.byOwner, st.owner)
		}
	}
}

func (lm *nfs4LockManager) newStateLocked(owner lockOwnerID, path string) *lockState {
	st := &lockState{owner: owner, path: path}
	lm.counter++
	binary.BigEndian.PutUint32(st.other[0:4], 0x4C4F434B) // "LOCK"
	binary.BigEndian.PutUint64(st.other[4:12], lm.counter)
	lm.states[st.other] = st
	if lm.byOwner[owner] == nil {
		lm.byOwner[owner] = make(map[string]*lockState)
	}
	lm.byOwner[owner][path] = st
	return st
}

// lock acquires or upgrades a range for (owner, path). On success it returns
// the lock state (stateid seqid already advanced). On conflict it returns the
// conflicting lock.
func (lm *nfs4LockManager) lock(owner lockOwnerID, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.lockLocked(owner, path, req)
}

// lockByStateID acquires a range using an existing lock stateid.
func (lm *nfs4LockManager) lockByStateID(other [nfs4OtherSize]byte, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	st, ok := lm.states[other]
	if !ok || st.path != path {
		return nil, nil, nfs4ErrBadStateID
	}
	return lm.lockLocked(st.owner, path, req)
}

func (lm *nfs4LockManager) lockLocked(owner lockOwnerID, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	if lm.locker == nil {
		return nil, nil, nfs4ErrNotSupp
	}
	lm.touchClient(owner.clientID)

	Log.Debugf("nfs4 LOCK owner={client:%x owner:%x} path=%q range=[%d,%d) type=%d",
		owner.clientID, owner.owner, path, req.start, req.end, req.lockType)

	conflict, err := lm.locker.Lock(owner.lockerOwner(), path, req.lockerRange())
	if err != nil {
		return nil, nil, lockerStatus(err)
	}
	if conflict != nil {
		return nil, conflictToDenied(conflict), nfs4ErrDenied
	}

	st := lm.byOwner[owner][path]
	if st == nil {
		st = lm.newStateLocked(owner, path)
	}
	st.seqid++
	return st, nil, nfs4OK
}

// unlock releases a range held under a lock stateid.
func (lm *nfs4LockManager) unlock(other [nfs4OtherSize]byte, path string, req lockRange) (*lockState, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if lm.locker == nil {
		return nil, nfs4ErrNotSupp
	}
	st, ok := lm.states[other]
	if !ok || st.path != path {
		return nil, nfs4ErrBadStateID
	}
	lm.touchClient(st.owner.clientID)

	if err := lm.locker.Unlock(st.owner.lockerOwner(), path, req.lockerRange()); err != nil {
		return nil, lockerStatus(err)
	}
	st.seqid++
	return st, nfs4OK
}

// test checks whether a range could be locked by owner (LOCKT).
func (lm *nfs4LockManager) test(owner lockOwnerID, path string, req lockRange) (*lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if lm.locker == nil {
		return nil, nfs4ErrNotSupp
	}
	lm.touchClient(owner.clientID)

	conflict, err := lm.locker.Test(owner.lockerOwner(), path, req.lockerRange())
	if err != nil {
		return nil, lockerStatus(err)
	}
	if conflict != nil {
		return conflictToDenied(conflict), nfs4ErrDenied
	}
	return nil, nfs4OK
}

// releaseOwner drops all lock state for an owner (RELEASE_LOCKOWNER).
func (lm *nfs4LockManager) releaseOwner(owner lockOwnerID) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.touchClient(owner.clientID)

	for _, st := range lm.byOwner[owner] {
		lm.dropStateLocked(st.other, st)
	}
	if lm.locker != nil {
		lm.locker.ReleaseOwner(owner.lockerOwner())
	}
}

// lockStateID renders the 16-byte stateid for a lock state snapshot.
func lockStateID(seqid uint32, other [nfs4OtherSize]byte) []byte {
	stateID := make([]byte, 16)
	binary.BigEndian.PutUint32(stateID[0:4], seqid)
	copy(stateID[4:], other[:])
	return stateID
}

// --- Server plumbing ---

// lockManager returns the per-server lock manager, creating it on first use
// around the server's Locker, or an in-memory table when it has none.
func (s *Server) lockManager() *nfs4LockManager {
	s.lockMgrOnce.Do(func() {
		locker := s.Locker
		if locker == nil {
			locker = NewMemoryLocker(0, 0)
		}
		s.lockMgr = newNFS4LockManager(locker)
	})
	return s.lockMgr
}

// --- Operation handlers ---

func writeLockDenied(wr *nfs4Writer, denied *lockDenied) {
	wr.writeUint64(denied.offset)
	wr.writeUint64(denied.length)
	wr.writeUint32(denied.lockType)
	wr.writeUint64(denied.owner.clientID)
	wr.writeOpaque([]byte(denied.owner.owner))
}

// nfs4OpLock implements LOCK (RFC 7530 section 16.10).
func nfs4OpLock(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	reclaim, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	newLockOwner, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}

	var owner lockOwnerID
	var existingOther [nfs4OtherSize]byte
	haveExisting := false
	if newLockOwner != 0 {
		// open_to_lock_owner4
		if _, err := rd.readUint32(); err != nil { // open_seqid
			return nfs4ErrBadXDR
		}
		if _, err := rd.readFixedOpaque(16); err != nil { // open_stateid
			return nfs4ErrBadXDR
		}
		if _, err := rd.readUint32(); err != nil { // lock_seqid
			return nfs4ErrBadXDR
		}
		clientID, err := rd.readUint64()
		if err != nil {
			return nfs4ErrBadXDR
		}
		ownerBytes, err := rd.readOpaque(nfs4OpaqueLimit)
		if err != nil {
			return nfs4ErrBadXDR
		}
		owner = lockOwnerID{clientID: clientID, owner: string(ownerBytes)}
	} else {
		// exist_lock_owner4
		stateID, err := rd.readFixedOpaque(16)
		if err != nil {
			return nfs4ErrBadXDR
		}
		if _, err := rd.readUint32(); err != nil { // lock_seqid
			return nfs4ErrBadXDR
		}
		copy(existingOther[:], stateID[4:16])
		haveExisting = true
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if reclaim != 0 {
		// No grace period: this server has no persistent lock state to
		// reclaim after restart.
		return nfs4ErrNoGrace
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	path := stringsJoinPath(current.path)
	lm := w.Server.lockManager()

	var st *lockState
	var denied *lockDenied
	if haveExisting {
		st, denied, status = lm.lockByStateID(existingOther, path, req)
	} else {
		st, denied, status = lm.lock(owner, path, req)
	}
	if status == nfs4ErrDenied {
		writeLockDenied(wr, denied)
		return nfs4ErrDenied
	}
	if status != nfs4OK {
		return status
	}

	wr.writeFixedOpaque(lockStateID(st.seqid, st.other))
	return nfs4OK
}

// nfs4OpLockT implements LOCKT (RFC 7530 section 16.11).
func nfs4OpLockT(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	clientID, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	ownerBytes, err := rd.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return nfs4ErrBadXDR
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	owner := lockOwnerID{clientID: clientID, owner: string(ownerBytes)}
	denied, status := w.Server.lockManager().test(owner, stringsJoinPath(current.path), req)
	if status == nfs4ErrDenied {
		writeLockDenied(wr, denied)
		return nfs4ErrDenied
	}
	return status
}

// nfs4OpLockU implements LOCKU (RFC 7530 section 16.12).
func nfs4OpLockU(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil { // seqid
		return nfs4ErrBadXDR
	}
	stateID, err := rd.readFixedOpaque(16)
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	var other [nfs4OtherSize]byte
	copy(other[:], stateID[4:16])
	st, status := w.Server.lockManager().unlock(other, stringsJoinPath(current.path), req)
	if status != nfs4OK {
		return status
	}

	wr.writeFixedOpaque(lockStateID(st.seqid, st.other))
	return nfs4OK
}

// Made with Bob
