package nfs

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

var (
	ownerA = LockOwner{Client: "nfs4/1", Owner: "a"}
	ownerB = LockOwner{Client: "nfs4/2", Owner: "b"}
)

type heldLock struct {
	LockRange
	Owner LockOwner
}

func newTestMemoryLocker(perFile, perClient int) *memoryLocker {
	return NewMemoryLocker(perFile, perClient).(*memoryLocker)
}

func mustMemLock(t *testing.T, m *memoryLocker, owner LockOwner, path string, start, end uint64, exclusive bool) {
	t.Helper()
	conflict, err := m.Lock(owner, path, LockRange{Start: start, End: end, Exclusive: exclusive})
	if err != nil || conflict != nil {
		t.Fatalf("Lock(%v, %s, [%d,%d)) = %+v, %v; want granted", owner, path, start, end, conflict, err)
	}
}

// heldOn lists the locks held on path, ordered by start and then owner.
func heldOn(m *memoryLocker, path string) []heldLock {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []heldLock
	for owner, ranges := range m.files[path] {
		for _, r := range ranges {
			out = append(out, heldLock{LockRange: r, Owner: owner})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return lessLockOwner(out[i].Owner, out[j].Owner)
	})
	return out
}

func assertHeld(t *testing.T, m *memoryLocker, path string, want []heldLock) {
	t.Helper()
	if got := heldOn(m, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("locks on %s = %+v, want %+v", path, got, want)
	}
}

func TestMemoryLockerConflictsBetweenOwners(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	mustMemLock(t, m, ownerA, "/f", 0, 100, true)

	conflict, err := m.Lock(ownerB, "/f", LockRange{Start: 50, End: 150, Exclusive: true})
	want := LockConflict{LockRange: LockRange{Start: 0, End: 100, Exclusive: true}, Owner: ownerA}
	if err != nil || conflict == nil || *conflict != want {
		t.Fatalf("overlapping exclusive lock = %+v, %v; want conflict %+v", conflict, err, want)
	}
	if conflict, _ := m.Lock(ownerB, "/f", LockRange{Start: 0, End: 10}); conflict == nil {
		t.Fatal("a read lock over another owner's write lock must conflict")
	}
	mustMemLock(t, m, ownerB, "/f", 200, 300, true)

	// Read locks coexist.
	mustMemLock(t, m, ownerA, "/g", 0, 10, false)
	mustMemLock(t, m, ownerB, "/g", 0, 10, false)

	// Refused requests granted nothing.
	assertHeld(t, m, "/f", []heldLock{
		{LockRange{Start: 0, End: 100, Exclusive: true}, ownerA},
		{LockRange{Start: 200, End: 300, Exclusive: true}, ownerB},
	})
}

func TestMemoryLockerReportsLowestConflict(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	mustMemLock(t, m, ownerA, "/f", 40, 50, true)
	mustMemLock(t, m, ownerA, "/f", 10, 20, true)
	for i := 0; i < 20; i++ {
		conflict, _ := m.Test(ownerB, "/f", LockRange{Start: 0, End: 100, Exclusive: true})
		if conflict == nil || conflict.Start != 10 {
			t.Fatalf("Test() = %+v, want the range starting at 10 every time", conflict)
		}
	}
}

func TestMemoryLockerSameOwnerReplacesAndUnlockSplits(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	mustMemLock(t, m, ownerA, "/f", 0, 100, true)
	// Downgrading the middle splits the write lock around a read lock.
	mustMemLock(t, m, ownerA, "/f", 40, 60, false)
	assertHeld(t, m, "/f", []heldLock{
		{LockRange{Start: 0, End: 40, Exclusive: true}, ownerA},
		{LockRange{Start: 40, End: 60}, ownerA},
		{LockRange{Start: 60, End: 100, Exclusive: true}, ownerA},
	})

	if err := m.Unlock(ownerA, "/f", LockRange{Start: 20, End: 80}); err != nil {
		t.Fatal(err)
	}
	assertHeld(t, m, "/f", []heldLock{
		{LockRange{Start: 0, End: 20, Exclusive: true}, ownerA},
		{LockRange{Start: 80, End: 100, Exclusive: true}, ownerA},
	})

	// Unlocking bytes the owner does not hold is not an error.
	if err := m.Unlock(ownerB, "/f", LockRange{Start: 0, End: 10}); err != nil {
		t.Fatalf("Unlock of unheld range = %v, want nil", err)
	}
}

func TestMemoryLockerTestExcludesRequestingOwner(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	mustMemLock(t, m, ownerA, "/f", 0, 10, true)
	if conflict, _ := m.Test(ownerA, "/f", LockRange{Start: 0, End: 10, Exclusive: true}); conflict != nil {
		t.Fatalf("an owner conflicts with its own lock: %+v", conflict)
	}
	if conflict, _ := m.Test(ownerB, "/f", LockRange{Start: 0, End: 10}); conflict == nil {
		t.Fatal("Test() missed another owner's lock")
	}
	assertHeld(t, m, "/f", []heldLock{{LockRange{Start: 0, End: 10, Exclusive: true}, ownerA}})
}

func TestMemoryLockerReleaseOwnerAndClient(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	sameClient := LockOwner{Client: ownerA.Client, Owner: "a2"}
	mustMemLock(t, m, ownerA, "/f", 0, 10, true)
	mustMemLock(t, m, ownerA, "/g", 0, 10, true)
	mustMemLock(t, m, sameClient, "/h", 0, 10, true)
	mustMemLock(t, m, ownerB, "/f", 20, 30, true)

	m.ReleaseOwner(ownerA)
	assertHeld(t, m, "/f", []heldLock{{LockRange{Start: 20, End: 30, Exclusive: true}, ownerB}})
	assertHeld(t, m, "/g", nil)
	assertHeld(t, m, "/h", []heldLock{{LockRange{Start: 0, End: 10, Exclusive: true}, sameClient}})

	m.ReleaseClient(ownerA.Client)
	assertHeld(t, m, "/h", nil)
	assertHeld(t, m, "/f", []heldLock{{LockRange{Start: 20, End: 30, Exclusive: true}, ownerB}})
	if n := m.clientLocks[ownerA.Client]; n != 0 {
		t.Fatalf("client count after release = %d, want 0", n)
	}
}

func TestMemoryLockerLimitsAreAtomic(t *testing.T) {
	m := newTestMemoryLocker(2, 0)
	mustMemLock(t, m, ownerA, "/f", 0, 10, true)
	mustMemLock(t, m, ownerA, "/f", 20, 30, true)
	// A third separate range exceeds the per-file cap and changes nothing.
	if _, err := m.Lock(ownerA, "/f", LockRange{Start: 40, End: 50, Exclusive: true}); !errors.Is(err, ErrLockLimit) {
		t.Fatalf("Lock over the file cap = %v, want ErrLockLimit", err)
	}
	assertHeld(t, m, "/f", []heldLock{
		{LockRange{Start: 0, End: 10, Exclusive: true}, ownerA},
		{LockRange{Start: 20, End: 30, Exclusive: true}, ownerA},
	})
	// Extending a held range does not add one, so it stays under the cap.
	mustMemLock(t, m, ownerA, "/f", 10, 20, true)
	assertHeld(t, m, "/f", []heldLock{{LockRange{Start: 0, End: 30, Exclusive: true}, ownerA}})

	c := newTestMemoryLocker(0, 1)
	mustMemLock(t, c, ownerA, "/f", 0, 10, true)
	if _, err := c.Lock(ownerA, "/g", LockRange{Start: 0, End: 10, Exclusive: true}); !errors.Is(err, ErrLockLimit) {
		t.Fatalf("Lock over the client cap = %v, want ErrLockLimit", err)
	}
	c.ReleaseOwner(ownerA)
	mustMemLock(t, c, ownerA, "/g", 0, 10, true)
}

func TestMemoryLockerRejectsEmptyRanges(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	if _, err := m.Lock(ownerA, "/f", LockRange{Start: 10, End: 10}); err == nil {
		t.Fatal("Lock of an empty range succeeded")
	}
	if _, err := m.Test(ownerA, "/f", LockRange{Start: 20, End: 10}); err == nil {
		t.Fatal("Test of an inverted range succeeded")
	}
	if err := m.Unlock(ownerA, "/f", LockRange{Start: 5, End: 5}); err == nil {
		t.Fatal("Unlock of an empty range succeeded")
	}
}

func TestMemoryLockerConcurrentOwners(t *testing.T) {
	m := newTestMemoryLocker(0, 0)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		owner := LockOwner{Client: fmt.Sprintf("nfs4/%d", i), Owner: "o"}
		wg.Add(1)
		go func(i int, owner LockOwner) {
			defer wg.Done()
			start := uint64(i * 10)
			for j := 0; j < 100; j++ {
				if c, err := m.Lock(owner, "/f", LockRange{Start: start, End: start + 10, Exclusive: true}); err != nil || c != nil {
					t.Errorf("owner %d: Lock = %+v, %v", i, c, err)
					return
				}
				if err := m.Unlock(owner, "/f", LockRange{Start: start, End: start + 10}); err != nil {
					t.Errorf("owner %d: Unlock = %v", i, err)
					return
				}
			}
		}(i, owner)
	}
	wg.Wait()
	assertHeld(t, m, "/f", nil)
}
