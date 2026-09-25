package nfs

import "io"

type nfs4OpenDowngradeArgs struct {
	StateID     nfs4StateID
	Seqid       uint32
	ShareAccess uint32
	ShareDeny   uint32
}

// nfs4OnOpenDowngrade has nothing to narrow, since opens take no share
// reservations, and only moves the open's stateid on.
func nfs4OnOpenDowngrade(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4OpenDowngradeArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if _, status := c.requireCurrent(); status != nfs4OK {
		return status
	}
	id, status := c.w.Server.nfs4State().updateOpen(req.StateID)
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, id)
}
