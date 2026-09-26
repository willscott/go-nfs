package nfs

import "io"

// nfs4OnPutRootFH serves PUTROOTFH and PUTPUBFH: both start at the root of
// what the handler mounts for "/".
func nfs4OnPutRootFH(c *nfs4Compound, _ io.Reader, _ io.Writer) nfs4Status {
	status, fs, _ := c.handler.Mount(c.ctx, c.w.conn, MountRequest{Dirpath: []byte("/")})
	if status != MountStatusOk || fs == nil {
		return nfs4ErrAccess
	}
	return c.setCurrent(fs, []string{})
}
