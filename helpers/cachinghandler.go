package helpers

import (
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"reflect"
	
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
	reverseCache:= make(map[string][][2]any)
	return &CachingHandler{
		Handler:         h,
		activeHandles:   cache,
		activeVerifiers: verifiers,
		cacheLimit:      limit,
		reverseCache: reverseCache,
	}
}

// CachingHandler implements to/from handle via an LRU cache.
type CachingHandler struct {
	nfs.Handler
	activeHandles   *lru.Cache[uuid.UUID, entry]
	activeVerifiers *lru.Cache[uint64, verifier]
	cacheLimit      int
	reverseCache map[string][][2]any
	
}

type entry struct {
	f billy.Filesystem
	p []string
}

// ToHandle takes a file and represents it with an opaque handle to reference it.
// In stateless nfs (when it's serving a unix fs) this can be the device + inode
// but we can generalize with a stateful local cache of handed out IDs.
func (c *CachingHandler) ToHandle(f billy.Filesystem, path []string) []byte {
	joinedPath:=f.Join(path...)
	
	handle, _, err :=c.searchReverseCache(f, path)
	if err==nil{
		return handle
	}
	
	id := uuid.New()
	
	newPath:=make([]string,len(path))
	
	copy(newPath,path)
	c.activeHandles.Add(id, entry{f, newPath})
	b, _ := id.MarshalBinary()
	c.reverseCache[joinedPath]=append(c.reverseCache[joinedPath],[2]any{f,b})
	
	return b
}

// FromHandle converts from an opaque handle to the file it represents
func (c *CachingHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	id, err := uuid.FromBytes(fh)
	if err != nil {
		return nil, []string{}, err
	}

	if f, ok := c.activeHandles.Get(id); ok {
		for _, k := range c.activeHandles.Keys() {
			candidate, _ := c.activeHandles.Peek(k)
			if hasPrefix(f.p, candidate.p) {
				_, _ = c.activeHandles.Get(k)
			}
		}
		if ok {
			newP:=make([]string,len(f.p))
			copy(newP,f.p)
			return f.f, newP, nil
		}
	}
	return nil, []string{}, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
}

func (c *CachingHandler) searchReverseCache (f billy.Filesystem, path []string) (handle []byte, idx int, err error){
	joinedPath:=f.Join(path...)
	pathToKey, exists := c.reverseCache[joinedPath]

	handle=make([]byte,0)
	err=fs.ErrInvalid
	idx = -1
	
	if !exists{
		c.reverseCache[joinedPath]=make([][2]any,0)
		return
	}
	
   for i, arr := range pathToKey{
		if reflect.DeepEqual(f,arr[0]){
			handle,_=arr[1].([]byte)
			idx=i
			err=nil
			return
		}
   }
   
   return	
}

func (c *CachingHandler) UpdateHandle( fs billy.Filesystem, fs2 billy.Filesystem, oldPath []string, newPath []string){
	handle,_,err:=c.searchReverseCache(fs,oldPath)
	if err!=nil{
		return
	}
	
	fs, oldPath, err = c.FromHandle(handle) //Normalize to what is in the cache(s)
	if err != nil{
		return
	}
	
	c.InvalidateHandle(fs,oldPath) //The oldPath no longer exists in fs
	
	//Associate the handle with the newPath
	id, _ := uuid.FromBytes(handle)
	c.activeHandles.Add(id,entry{fs2,newPath})
	c.reverseCache[fs2.Join(newPath...)]=append(c.reverseCache[fs2.Join(newPath...)],[2]any{fs2,handle})
}

func (c *CachingHandler) InvalidateHandle( fs billy.Filesystem, path []string){
	handle, i, err := c.searchReverseCache(fs,path)
	if err!=nil{
		return
	}
	
	//Remove from reverseCache
	joinedPath:=fs.Join(path...)
	arr:=c.reverseCache[joinedPath]
	arr[i]=arr[len(arr)-1]
	c.reverseCache[joinedPath]=arr[:len(arr)-1]
	
	//Remove from cache
	id, _ := uuid.FromBytes(handle)
	c.activeHandles.Remove(id)
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
