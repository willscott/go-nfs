package nfs

import "io"

type nfs4CloseArgs struct {
	Seqid   uint32
	StateID nfs4StateID
}

func nfs4OnClose(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4CloseArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if _, status := c.requireCurrent(); status != nfs4OK {
		return status
	}
	id, status := c.w.Server.nfs4State().close(req.StateID)
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, id)
}
