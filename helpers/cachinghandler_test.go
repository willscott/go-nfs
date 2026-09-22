package helpers

import (
	"fmt"
	"sync"
	"testing"

	"github.com/willscott/go-nfs/helpers/memfs"
)

// TestCachingHandlerConcurrentToHandle tests that concurrent calls to ToHandle
// are thread-safe. Run with -race flag to detect data races:
//
//	go test -race -run TestCachingHandlerConcurrentToHandle ./helpers/
func TestCachingHandlerConcurrentToHandle(t *testing.T) {
	mem := memfs.New()
	handler := NewNullAuthHandler(mem)
	cacheHandler := NewCachingHandler(handler, 1024).(*CachingHandler)

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Each goroutine creates handles for different paths
				// but also accesses some shared paths to maximize contention
				path := []string{fmt.Sprintf("file-%d-%d.txt", id, j)}
				_ = cacheHandler.ToHandle(mem, path)

				// Also access a shared path to increase contention
				sharedPath := []string{fmt.Sprintf("shared-%d.txt", j%10)}
				_ = cacheHandler.ToHandle(mem, sharedPath)
			}
		}(i)
	}

	wg.Wait()
}

// TestCachingHandlerConcurrentToHandleAndFromHandle tests concurrent access
// to both ToHandle and FromHandle methods.
func TestCachingHandlerConcurrentToHandleAndFromHandle(t *testing.T) {
	mem := memfs.New()
	handler := NewNullAuthHandler(mem)
	cacheHandler := NewCachingHandler(handler, 1024).(*CachingHandler)

	const numGoroutines = 10
	const numOperations = 100

	// Pre-create some handles
	handles := make([][]byte, 20)
	for i := 0; i < 20; i++ {
		path := []string{fmt.Sprintf("precreated-%d.txt", i)}
		handles[i] = cacheHandler.ToHandle(mem, path)
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers - create new handles
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				path := []string{fmt.Sprintf("new-file-%d-%d.txt", id, j)}
				_ = cacheHandler.ToHandle(mem, path)
			}
		}(i)
	}

	// Readers - read existing handles
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				handle := handles[j%len(handles)]
				_, _, _ = cacheHandler.FromHandle(handle)
			}
		}(i)
	}

	wg.Wait()
}

// TestCachingHandlerConcurrentInvalidateHandle tests concurrent access
// when handles are being invalidated.
func TestCachingHandlerConcurrentInvalidateHandle(t *testing.T) {
	mem := memfs.New()
	handler := NewNullAuthHandler(mem)
	cacheHandler := NewCachingHandler(handler, 1024).(*CachingHandler)

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Create and invalidate handles concurrently
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				path := []string{fmt.Sprintf("invalidate-%d-%d.txt", id, j)}
				handle := cacheHandler.ToHandle(mem, path)
				// Immediately invalidate some handles
				if j%3 == 0 {
					_ = cacheHandler.InvalidateHandle(mem, handle)
				}
			}
		}(i)
	}

	// Concurrent ToHandle calls on shared paths
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				sharedPath := []string{fmt.Sprintf("shared-invalidate-%d.txt", j%20)}
				_ = cacheHandler.ToHandle(mem, sharedPath)
			}
		}(i)
	}

	wg.Wait()
}

// An export's root handle must keep working however many other handles the
// server hands out: an NFSv3 client holds the one it got at mount for the
// life of that mount, so losing it to the cache makes every path under it
// stale and the mount unusable.
func TestCachingHandlerKeepsRootHandle(t *testing.T) {
	fs := memfs.New()
	if err := fs.MkdirAll("/", 0o755); err != nil {
		t.Fatal(err)
	}
	h := NewCachingHandler(NewNullAuthHandler(fs), 8)

	root := h.ToHandle(fs, []string{})
	if same := h.ToHandle(fs, []string{}); string(same) != string(root) {
		t.Fatal("the root was given a second handle")
	}

	for i := 0; i < 100; i++ {
		h.ToHandle(fs, []string{"dir", fmt.Sprint(i)})
	}

	gotFS, path, err := h.FromHandle(root)
	if err != nil {
		t.Fatalf("root handle went stale after 100 other handles: %v", err)
	}
	if gotFS != fs || len(path) != 0 {
		t.Fatalf("root handle resolved to %v, want the export root", path)
	}
}
