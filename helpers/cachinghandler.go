package helpers

import (
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"reflect"
	"sync"

	"github.com/willscott/go-nfs"

	"github.com/go-git/go-billy/v5"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
)

// NewCachingHandler wraps a handler to provide a basic to/from-file handle cache.
func NewCachingHandler(h nfs.Handler, limit int) nfs.Handler {
	return NewCachingHandlerWithVerifierLimit(h, limit, limit)
}

// NewCachingHandlerWithVerifierLimit provides a basic to/from-file handle cache that can be tuned with a smaller cache of active directory listings.
func NewCachingHandlerWithVerifierLimit(h nfs.Handler, limit int, verifierLimit int) nfs.Handler {
	if limit < 2 || verifierLimit < 2 {
		nfs.Log.Warnf("Caching handler created with insufficient cache to support directory listing", "size", limit, "verifiers", verifierLimit)
	}
	cache, _ := lru.New[uuid.UUID, entry](limit)
	verifiers, _ := lru.New[uint64, verifier](verifierLimit)
	return &CachingHandler{
		Handler:         h,
		activeHandles:   cache,
		reverseHandles:  make(map[string][]uuid.UUID),
		rootHandles:     make(map[uuid.UUID]entry),
		rootByPath:      make(map[string]uuid.UUID),
		activeVerifiers: verifiers,
		cacheLimit:      limit,
	}
}

// CachingHandler implements to/from handle via an LRU cache.
type CachingHandler struct {
	nfs.Handler
	activeHandles    *lru.Cache[uuid.UUID, entry]
	reverseHandles   map[string][]uuid.UUID
	reverseHandlesMu sync.RWMutex
	activeVerifiers  *lru.Cache[uint64, verifier]
	cacheLimit       int
	// rootHandles are the handles of export roots, which are never evicted.
	// An NFSv3 client holds the root handle it got at mount for as long as
	// that mount lives, so losing it to the cache makes every path under it
	// stale and the mount unusable until it is mounted again. NFSv4 clients
	// re-resolve the root with PUTROOTFH and do not notice.
	rootHandles map[uuid.UUID]entry
	rootByPath  map[string]uuid.UUID
	rootMu      sync.RWMutex
}

// isRoot reports whether path names an export's root.
func isRoot(path []string) bool {
	for _, p := range path {
		if p != "" && p != "." && p != "/" {
			return false
		}
	}
	return true
}

// rootHandle returns the pinned handle for an export root, taking one the
// first time the root is asked for.
func (c *CachingHandler) rootHandle(f billy.Filesystem, joinedPath string, path []string) []byte {
	c.rootMu.RLock()
	if id, ok := c.rootByPath[joinedPath]; ok {
		entry, ok := c.rootHandles[id]
		c.rootMu.RUnlock()
		if ok && reflect.DeepEqual(entry.f, f) {
			return id[:]
		}
	} else {
		c.rootMu.RUnlock()
	}

	c.rootMu.Lock()
	defer c.rootMu.Unlock()
	if id, ok := c.rootByPath[joinedPath]; ok {
		if entry, ok := c.rootHandles[id]; ok && reflect.DeepEqual(entry.f, f) {
			return id[:]
		}
	}
	id := uuid.New()
	newPath := make([]string, len(path))
	copy(newPath, path)
	c.rootHandles[id] = entry{f, newPath}
	c.rootByPath[joinedPath] = id
	b, _ := id.MarshalBinary()
	return b
}

// fromRootHandle resolves a pinned export root handle.
func (c *CachingHandler) fromRootHandle(id uuid.UUID) (entry, bool) {
	c.rootMu.RLock()
	defer c.rootMu.RUnlock()
	e, ok := c.rootHandles[id]
	return e, ok
}

type entry struct {
	f billy.Filesystem
	p []string
}

// ToHandle takes a file and represents it with an opaque handle to reference it.
// In stateless nfs (when it's serving a unix fs) this can be the device + inode
// but we can generalize with a stateful local cache of handed out IDs.
func (c *CachingHandler) ToHandle(f billy.Filesystem, path []string) []byte {
	joinedPath := f.Join(path...)

	if isRoot(path) {
		return c.rootHandle(f, joinedPath, path)
	}

	if handle := c.searchReverseCache(f, joinedPath); handle != nil {
		return handle
	}

	id := uuid.New()

	newPath := make([]string, len(path))

	copy(newPath, path)
	evictedKey, evictedPath, ok := c.activeHandles.GetOldest()
	if evicted := c.activeHandles.Add(id, entry{f, newPath}); evicted && ok {
		rk := evictedPath.f.Join(evictedPath.p...)
		c.evictReverseCache(rk, evictedKey)
	}

	c.appendReverseHandle(joinedPath, id)
	b, _ := id.MarshalBinary()

	return b
}

// FromHandle converts from an opaque handle to the file it represents
func (c *CachingHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	id, err := uuid.FromBytes(fh)
	if err != nil {
		return nil, []string{}, err
	}

	if e, ok := c.fromRootHandle(id); ok {
		newP := make([]string, len(e.p))
		copy(newP, e.p)
		return e.f, newP, nil
	}

	if f, ok := c.activeHandles.Get(id); ok {
		c.touchAncestors(f)
		newP := make([]string, len(f.p))
		copy(newP, f.p)
		return f.f, newP, nil
	}
	return nil, []string{}, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
}

// touchAncestors marks the handles of e's parent directories as recently
// used, so a directory is not evicted while files under it are in use.
// Looking them up by path costs the depth of the path; walking the whole
// cache, as this used to, cost an operation per cached handle on every
// request.
func (c *CachingHandler) touchAncestors(e entry) {
	for i := len(e.p) - 1; i > 0; i-- {
		for _, id := range c.getReverseHandles(e.f.Join(e.p[:i]...)) {
			if candidate, ok := c.activeHandles.Get(id); ok && reflect.DeepEqual(candidate.f, e.f) {
				break
			}
		}
	}
}

func (c *CachingHandler) searchReverseCache(f billy.Filesystem, path string) []byte {
	uuids := c.getReverseHandles(path)

	for _, id := range uuids {
		if candidate, ok := c.activeHandles.Get(id); ok {
			if reflect.DeepEqual(candidate.f, f) {
				return id[:]
			}
		}
	}

	return nil
}

func (c *CachingHandler) evictReverseCache(path string, handle uuid.UUID) {
	c.reverseHandlesMu.Lock()
	defer c.reverseHandlesMu.Unlock()

	uuids, ok := c.reverseHandles[path]
	if !ok {
		return
	}
	for i, u := range uuids {
		if u == handle {
			c.reverseHandles[path] = append(uuids[:i], uuids[i+1:]...)
			return
		}
	}
}

func (c *CachingHandler) getReverseHandles(path string) []uuid.UUID {
	c.reverseHandlesMu.RLock()
	defer c.reverseHandlesMu.RUnlock()
	return c.reverseHandles[path]
}

func (c *CachingHandler) appendReverseHandle(path string, id uuid.UUID) {
	c.reverseHandlesMu.Lock()
	defer c.reverseHandlesMu.Unlock()
	c.reverseHandles[path] = append(c.reverseHandles[path], id)
}

func (c *CachingHandler) InvalidateHandle(fs billy.Filesystem, handle []byte) error {
	//Remove from cache
	id, _ := uuid.FromBytes(handle)
	entry, ok := c.activeHandles.Get(id)
	if ok {
		rk := entry.f.Join(entry.p...)
		c.evictReverseCache(rk, id)
	}
	c.activeHandles.Remove(id)
	return nil
}

// HandleLimit exports how many file handles can be safely stored by this cache.
func (c *CachingHandler) HandleLimit() int {
	return c.cacheLimit
}

type verifier struct {
	path     string
	contents []fs.FileInfo
}

func hashPathAndContents(path string, contents []fs.FileInfo) uint64 {
	//calculate a cookie-verifier.
	vHash := sha256.New()

	// Add the path to avoid collisions of directories with the same content
	vHash.Write(binary.BigEndian.AppendUint64([]byte{}, uint64(len(path))))
	vHash.Write([]byte(path))

	for _, c := range contents {
		vHash.Write([]byte(c.Name())) // Never fails according to the docs
	}

	verify := vHash.Sum(nil)[0:8]
	return binary.BigEndian.Uint64(verify)
}

func (c *CachingHandler) VerifierFor(path string, contents []fs.FileInfo) uint64 {
	id := hashPathAndContents(path, contents)
	c.activeVerifiers.Add(id, verifier{path, contents})
	return id
}

func (c *CachingHandler) DataForVerifier(path string, id uint64) []fs.FileInfo {
	if cache, ok := c.activeVerifiers.Get(id); ok {
		return cache.contents
	}
	return nil
}
