Golang Network File Server
===

A [WIP] implementation of the NFSv3 protocol in pure Golang.

Notes
---

* Ports are typically determined through portmap. The need for running portmap 
(which is the only part that needs a privileged listening port) can be avoided
through specific mount options. e.g. 
`mount -o port=n,mountport=n -t nfs host:/mount /localmount`

* It's unfortunate there isn't a agnostic / standard VFS interface to structure
the backing of the server around. The closest may be to use the interface from
[`net/http.FileSystem`](https://github.com/golang/go/blob/go1.11.2/src/net/http/fs.go#L93)
or to give up on being agnostic and go with [`rio`](https://github.com/polydawn/rio/blob/master/fs/interface.go).
