package helpers

import (
	"io/fs"

	"github.com/willscott/go-nfs"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
)

// NewCachingHandler wraps a handler to provide a basic to/from-file handle cache.
func NewCachingHandler(h nfs.Handler, limit int) nfs.Handler {
	cache, _ := lru.New(limit)
	return &CachingHandler{
		Handler:       h,
		activeHandles: cache,
		cacheLimit:    limit,
	}
}

// CachingHandler implements to/from handle via an LRU cache.
type CachingHandler struct {
	nfs.Handler
	activeHandles *lru.Cache
	cacheLimit    int
}

type entry struct {
	f fs.FS
	p []string
}

// ToHandle takes a file and represents it with an opaque handle to reference it.
// In stateless nfs (when it's serving a unix fs) this can be the device + inode
// but we can generalize with a stateful local cache of handed out IDs.
func (c *CachingHandler) ToHandle(f fs.FS, path []string) []byte {
	id := uuid.New()
	c.activeHandles.Add(id, entry{f, path})
	b, _ := id.MarshalBinary()
	return b
}

// FromHandle converts from an opaque handle to the file it represents
func (c *CachingHandler) FromHandle(fh []byte) (fs.FS, []string, error) {
	id, err := uuid.FromBytes(fh)
	if err != nil {
		return nil, []string{}, err
	}

	if cache, ok := c.activeHandles.Get(id); ok {
		f, ok := cache.(entry)
		for _, k := range c.activeHandles.Keys() {
			e, _ := c.activeHandles.Peek(k)
			candidate := e.(entry)
			if hasPrefix(f.p, candidate.p) {
				_, _ = c.activeHandles.Get(k)
			}
		}
		if ok {
			return f.f, f.p, nil
		}
	}
	return nil, []string{}, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
}

// HandleLimit exports how many file handles can be safely stored by this cache.
func (c *CachingHandler) HandleLimit() int {
	return c.cacheLimit
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, e := range prefix {
		if path[i] != e {
			return false
		}
	}
	return true
}
