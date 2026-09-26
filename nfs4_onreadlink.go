package nfs

import "io"

func nfs4OnReadLink(c *nfs4Compound, _ io.Reader, res io.Writer) nfs4Status {
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	target, err := current.fs.Readlink(current.fullPath())
	if err != nil {
		return nfs4StatusFromErr(err)
	}
	return nfs4Encode(res, target)
}
