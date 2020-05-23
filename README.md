Golang Network File Server
===

NFSv3 protocol implementation in pure Golang.

Current Status:
* Untested
* Mounts, read-only behavior works
* Writing/mutations not fully implemented

Usage
===

The NFS server runs on a `net.Listener` to export a file system to NFS clients.
Usage is structured similarly to many other golang network servers.

```golang
import (
   	"github.com/go-git/go-billy/v5/memfs"

	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

listener, _ := net.Listen("tcp", ":0")
fmt.Printf("Server running at %s\n", listener.Addr())

mem := memfs.New()
f, err := mem.Create("hello.txt")
f.Write([]byte("hello world"))
f.Close()

handler := nfshelper.NewNullAuthHandler(mem)
cacheHelper := nfshelper.NewCachingHandler(handler)
nfs.Serve(listener, cacheHelper)
```


Notes
---

* Ports are typically determined through portmap. The need for running portmap 
(which is the only part that needs a privileged listening port) can be avoided
through specific mount options. e.g. 
`mount -o port=n,mountport=n -t nfs host:/mount /localmount`

* This server currently uses [billy](https://github.com/go-git/go-billy/) to
provide a file system abstraction layer. There are some edges of the NFS protocol
which do not translate to this abstraction.
  * NFS expects access to an `inode` or equivalent unique identifier to reference
  files in a file system. These are considered opaque identifiers here, which
  means they will not work as expected in cases of hard linking.
  * The billy abstraction layer does not extend to exposing `uid` and `gid`
  ownership of files. If ownership is important to your file system, you
  will need to ensure that the `os.FileInfo` meets additional constraints.
  In particular, the `Sys()` escape hatch is queried by this library, and
  if your file system populates a [`syscall.Stat_t`](https://golang.org/pkg/syscall/#Stat_t)
  concrete struct, the ownership specified in that object will be used.

* Relevant RFCS:
[5531 - RPC protocol](https://tools.ietf.org/html/rfc5531),
[1813 - NFSv3](https://tools.ietf.org/html/rfc1813),
[1094 - NFS](https://tools.ietf.org/html/rfc1094)
