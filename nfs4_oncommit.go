package nfs

import "io"

type nfs4CommitArgs struct {
	Offset uint64
	Count  uint32
}

func nfs4OnCommit(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4CommitArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if _, status := c.requireCurrent(); status != nfs4OK {
		return status
	}
	// Every WRITE is FILE_SYNC, so there is nothing to commit.
	return nfs4Encode(res, c.w.Server.ID)
}
