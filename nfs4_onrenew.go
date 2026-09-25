package nfs

import "io"

type nfs4RenewArgs struct {
	ClientID uint64
}

func nfs4OnRenew(c *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4RenewArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	c.w.Server.nfs4State().renewClient(req.ClientID)
	return nfs4OK
}
