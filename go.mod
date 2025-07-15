module github.com/treeverse/go-nfs

go 1.23.0

require (
	github.com/google/uuid v1.6.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/rasky/go-xdr v0.0.0-20170124162913-1a41d1a06c93
	github.com/willscott/go-nfs v0.0.3
	github.com/willscott/go-nfs-client v0.0.0-20240104095149-b44639837b00
	golang.org/x/sys v0.34.0
)

require (
	github.com/cyphar/filepath-securejoin v0.4.1 // indirect
	github.com/go-git/go-billy/v6 v6.0.0-20250711053805-c1f149aaab07 // indirect
	github.com/polydawn/go-timeless-api v0.0.0-20220821201550-b93919e12c56 // indirect
	github.com/polydawn/refmt v0.0.0-20201211092308-30ac6d18308e // indirect
	github.com/polydawn/rio v0.0.0-20220823181337-7c31ad9831a4 // indirect
	github.com/warpfork/go-errcat v0.0.0-20180917083543-335044ffc86e // indirect
)

replace github.com/willscott/go-nfs => .
