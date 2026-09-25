package nfs

import "io"

func nfs4OnRestoreFH(c *nfs4Compound, _ io.Reader, _ io.Writer) nfs4Status {
	if c.saved == nil {
		return nfs4ErrNoFileHandle
	}
	c.current = c.saved.clone()
	return nfs4OK
}
