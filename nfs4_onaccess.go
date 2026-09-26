package nfs

import "io"

const (
	nfs4AccessRead    uint32 = 0x00000001
	nfs4AccessLookup  uint32 = 0x00000002
	nfs4AccessModify  uint32 = 0x00000004
	nfs4AccessExtend  uint32 = 0x00000008
	nfs4AccessDelete  uint32 = 0x00000010
	nfs4AccessExecute uint32 = 0x00000020
)

type nfs4AccessArgs struct {
	Access uint32
}

type nfs4AccessRes struct {
	Supported uint32
	Access    uint32
}

func nfs4OnAccess(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4AccessArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	supported := nfs4AccessRead | nfs4AccessLookup | nfs4AccessModify | nfs4AccessExtend | nfs4AccessDelete | nfs4AccessExecute
	granted := supported
	if current.ensureWritable() != nfs4OK {
		granted = nfs4AccessRead | nfs4AccessLookup | nfs4AccessExecute
	}
	return nfs4Encode(res, nfs4AccessRes{Supported: supported, Access: req.Access & granted})
}
