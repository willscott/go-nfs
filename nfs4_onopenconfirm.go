package nfs

import "io"

type nfs4OpenConfirmArgs struct {
	StateID nfs4StateID
	Seqid   uint32
}

func nfs4OnOpenConfirm(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4OpenConfirmArgs
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
