package nfs

import "io"

type nfs4RemoveArgs struct {
	Name string
}

func nfs4OnRemove(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4RemoveArgs
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
	before := current.changeID()
	if err := current.fs.Remove(nfs4Join(current.fs, current.child(req.Name))); err != nil {
		return nfs4StatusFromErr(err)
	}
	after := current.changeID()
	return nfs4Encode(res, nfs4ChangeInfo{Before: before, After: after})
}
