package nfs

import "io"

type nfs4LookupArgs struct {
	Name string
}

func nfs4OnLookup(c *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4LookupArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if status := nfs4Component(req.Name); status != nfs4OK {
		return status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := current.ensureDir(); status != nfs4OK {
		return status
	}

	childPath := current.child(req.Name)
	if _, err := current.fs.Lstat(nfs4Join(current.fs, childPath)); err != nil {
		return nfs4StatusFromErr(err)
	}
	return c.setCurrent(current.fs, childPath)
}
