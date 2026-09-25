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
	handle := c.handler.ToHandle(current.fs, current.child(req.Name))
	before := current.changeID()
	if err := current.fs.Remove(nfs4Join(current.fs, current.child(req.Name))); err != nil {
		return nfs4StatusFromErr(err)
	}
	if err := c.handler.InvalidateHandle(current.fs, handle); err != nil {
		return nfs4ErrServerFault
	}
	after := current.changeID()
	return nfs4Encode(res, nfs4ChangeInfo{Before: before, After: after})
}
