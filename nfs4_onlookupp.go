package nfs

import "io"

func nfs4OnLookupP(c *nfs4Compound, _ io.Reader, _ io.Writer) nfs4Status {
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if len(current.path) == 0 {
		return nfs4ErrNoEnt
	}
	return c.setCurrent(current.fs, nfs4CopyPath(current.path[:len(current.path)-1]))
}
