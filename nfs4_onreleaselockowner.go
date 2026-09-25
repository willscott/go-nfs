package nfs

import "io"

type nfs4ReleaseLockOwnerArgs struct {
	Owner nfs4Owner
}

func nfs4OnReleaseLockOwner(c *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4ReleaseLockOwnerArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	c.w.Server.nfs4State().releaseOwner(req.Owner)
	return nfs4OK
}
