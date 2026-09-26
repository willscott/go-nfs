package nfs

import "io"

func nfs4OnGetFH(c *nfs4Compound, _ io.Reader, res io.Writer) nfs4Status {
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, current.handle)
}
