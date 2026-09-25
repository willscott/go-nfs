package helpers

import "github.com/willscott/go-nfs"

// RecoverPanics installs nfs.DefaultPanicHandler on srv if PanicHandler is unset, returning srv for chaining.
func RecoverPanics(srv *nfs.Server) *nfs.Server {
	if srv.PanicHandler == nil {
		srv.PanicHandler = nfs.DefaultPanicHandler
	}
	return srv
}
