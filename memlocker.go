package nfs

import (
	"errors"
	"sort"
	"sync"
)

// Default caps for NewMemoryLocker bound lock state per file and per client,
// so a misbehaving or leaky client cannot grow server memory without bound.
const (
	DefaultMaxLocksPerFile   = 512
	DefaultMaxLocksPerClient = 8192
)

var errInvalidLockRange = errors.New("nfs: empty or inverted lock range")

// NewMemoryLocker returns an in-memory ByteRangeLocker. It is what a Server
// uses when its Locker is nil. Locks are advisory and do not survive a
// restart. Caps of zero or less select the defaults.
func NewMemoryLocker(maxLocksPerFile, maxLocksPerClient int) ByteRangeLocker {
	if maxLocksPerFile <= 0 {
		maxLocksPerFile = DefaultMaxLocksPerFile
	}
	if maxLocksPerClient <= 0 {
		maxLocksPerClient = DefaultMaxLocksPerClient
	}
	return &memoryLocker{
		maxLocksPerFile:   maxLocksPerFile,
		maxLocksPerClient: maxLocksPerClient,
		files:             make(map[string]map[LockOwner][]LockRange),
		owned:             make(map[LockOwner]map[string]struct{}),
		clientLocks:       make(map[string]int),
	}
}

type memoryLocker struct {
	mu                sync.Mutex
	maxLocksPerFile   int
	maxLocksPerClient int

	// files maps a path to each owner's held ranges on it, sorted by start.
	files map[string]map[LockOwner][]LockRange
	// owned indexes the paths each owner holds ranges on.
	owned map[LockOwner]map[string]struct{}
	// clientLocks counts held ranges per client for the client cap.
	clientLocks map[string]int
}

// Lock grants r to owner. The owner's own overlapping ranges on path are
// replaced, so a holder can upgrade, downgrade, or split its locks. Granting
// is atomic: on ErrLockLimit the owner's ranges are unchanged.
func (m *memoryLocker) Lock(owner LockOwner, path string, r LockRange) (*LockConflict, error) {
	if r.Start >= r.End {
		return nil, errInvalidLockRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if conflict := m.conflictLocked(owner, path, r); conflict != nil {
		return conflict, nil
	}
	before := m.files[path][owner]
	after := coalesceRanges(append(subtractRange(before, r), r))
	delta := len(after) - len(before)
	if delta > 0 && (m.fileLockCountLocked(path)+delta > m.maxLocksPerFile ||
		m.clientLocks[owner.Client]+delta > m.maxLocksPerClient) {
		return nil, ErrLockLimit
	}
	m.setLocked(owner, path, after, delta)
	return nil, nil
}

// Unlock releases r from owner's ranges on path, splitting ranges that
// straddle it. Releasing bytes the owner does not hold is not an error.
func (m *memoryLocker) Unlock(owner LockOwner, path string, r LockRange) error {
	if r.Start >= r.End {
		return errInvalidLockRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	before := m.files[path][owner]
	after := subtractRange(before, r)
	m.setLocked(owner, path, after, len(after)-len(before))
	return nil
}

func (m *memoryLocker) Test(owner LockOwner, path string, r LockRange) (*LockConflict, error) {
	if r.Start >= r.End {
		return nil, errInvalidLockRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conflictLocked(owner, path, r), nil
}

func (m *memoryLocker) ReleaseOwner(owner LockOwner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseOwnerLocked(owner)
}

func (m *memoryLocker) ReleaseClient(client string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for owner := range m.owned {
		if owner.Client == client {
			m.releaseOwnerLocked(owner)
		}
	}
}

// conflictLocked returns the conflicting lock with the lowest start held by
// another owner, so denials describe the same lock every time.
func (m *memoryLocker) conflictLocked(owner LockOwner, path string, r LockRange) *LockConflict {
	var found *LockConflict
	for other, ranges := range m.files[path] {
		if other == owner {
			continue
		}
		for _, h := range ranges {
			if !rangesOverlap(h, r) || (!h.Exclusive && !r.Exclusive) {
				continue
			}
			if found == nil || h.Start < found.Start ||
				(h.Start == found.Start && lessLockOwner(other, found.Owner)) {
				found = &LockConflict{LockRange: h, Owner: other}
			}
		}
	}
	return found
}

func (m *memoryLocker) fileLockCountLocked(path string) int {
	count := 0
	for _, ranges := range m.files[path] {
		count += len(ranges)
	}
	return count
}

// setLocked stores owner's ranges on path and keeps the indexes and client
// count in step; delta is the change in the owner's range count.
func (m *memoryLocker) setLocked(owner LockOwner, path string, ranges []LockRange, delta int) {
	owners := m.files[path]
	if len(ranges) == 0 {
		delete(owners, owner)
		if len(owners) == 0 {
			delete(m.files, path)
		}
		if paths := m.owned[owner]; paths != nil {
			delete(paths, path)
			if len(paths) == 0 {
				delete(m.owned, owner)
			}
		}
	} else {
		if owners == nil {
			owners = make(map[LockOwner][]LockRange)
			m.files[path] = owners
		}
		owners[owner] = ranges
		if m.owned[owner] == nil {
			m.owned[owner] = make(map[string]struct{})
		}
		m.owned[owner][path] = struct{}{}
	}

	if n := m.clientLocks[owner.Client] + delta; n > 0 {
		m.clientLocks[owner.Client] = n
	} else {
		delete(m.clientLocks, owner.Client)
	}
}

func (m *memoryLocker) releaseOwnerLocked(owner LockOwner) {
	for path := range m.owned[owner] {
		m.setLocked(owner, path, nil, -len(m.files[path][owner]))
	}
}

func rangesOverlap(a, b LockRange) bool {
	return a.Start < b.End && b.Start < a.End
}

// subtractRange returns ranges with sub removed, splitting ranges that
// straddle it. It never modifies its input, which keeps a refused Lock atomic.
func subtractRange(ranges []LockRange, sub LockRange) []LockRange {
	out := make([]LockRange, 0, len(ranges)+1)
	for _, h := range ranges {
		if !rangesOverlap(h, sub) {
			out = append(out, h)
			continue
		}
		if h.Start < sub.Start {
			out = append(out, LockRange{Start: h.Start, End: sub.Start, Exclusive: h.Exclusive})
		}
		if sub.End < h.End {
			out = append(out, LockRange{Start: sub.End, End: h.End, Exclusive: h.Exclusive})
		}
	}
	return out
}

// coalesceRanges sorts ranges by start and merges adjacent or overlapping
// ranges of the same mode, bounding growth from repeated small locks.
func coalesceRanges(ranges []LockRange) []LockRange {
	if len(ranges) < 2 {
		return ranges
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	out := ranges[:1]
	for _, h := range ranges[1:] {
		last := &out[len(out)-1]
		if h.Exclusive == last.Exclusive && h.Start <= last.End {
			if h.End > last.End {
				last.End = h.End
			}
			continue
		}
		out = append(out, h)
	}
	return out
}

func lessLockOwner(a, b LockOwner) bool {
	if a.Client != b.Client {
		return a.Client < b.Client
	}
	return a.Owner < b.Owner
}
