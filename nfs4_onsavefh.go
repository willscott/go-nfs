package nfs

import "io"

func nfs4OnSaveFH(c *nfs4Compound, _ io.Reader, _ io.Writer) nfs4Status {
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	c.saved = current.clone()
	return nfs4OK
}
