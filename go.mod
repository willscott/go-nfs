module github.com/willscott/go-nfs

go 1.13

require (
	github.com/go-git/go-billy/v5 v5.0.0
	github.com/google/uuid v1.1.1
	github.com/hashicorp/golang-lru v0.5.4
	github.com/polydawn/refmt v0.0.0-20190807091052-3d65705ee9f1 // indirect
	github.com/rasky/go-xdr v0.0.0-20170124162913-1a41d1a06c93
	github.com/willscott/go-nfs-client v0.0.0-20200605172546-271fa9065b33
	github.com/willscott/memphis v0.0.0-20200912203731-93fad52b5f1f
	go.polydawn.net/go-timeless-api v0.0.0-00010101000000-000000000000 // indirect
	go.polydawn.net/rio v0.0.0-00010101000000-000000000000
)

replace go.polydawn.net/go-timeless-api => github.com/polydawn/go-timeless-api v0.0.0-20190707220600-0ece408663ed

replace go.polydawn.net/rio => github.com/polydawn/rio v0.0.0-20200325050149-e97d9995e350
