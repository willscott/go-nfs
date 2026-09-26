package nfs

import "io"

type nfs4SetClientIDConfirmArgs struct {
	ClientID uint64
	Confirm  [8]byte
}

func nfs4OnSetClientIDConfirm(c *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4SetClientIDConfirmArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	return c.w.Server.nfs4State().confirmClientID(req.ClientID, req.Confirm)
}
