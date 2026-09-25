package nfs

import (
	"io"
	"os"
)

type nfs4WriteArgs struct {
	StateID nfs4StateID
	Offset  uint64
	Stable  uint32
	Data    []byte
}

type nfs4WriteRes struct {
	Count     uint32
	Committed uint32
	Verf      [8]byte
}

func nfs4OnWrite(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4WriteArgs
	if err := xdrReadLimited(args, &req, nfs4MaxWrite); err != nil {
		return nfs4ErrBadXDR
	}
	if req.Stable > uint32(fileSync) {
		return nfs4ErrInval
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	c.w.Server.nfs4State().renewState(req.StateID)
	info, err := current.fs.Stat(current.fullPath())
	if err != nil {
		return nfs4StatusFromErr(err)
	}
	if !info.Mode().IsRegular() {
		return nfs4ErrInval
	}
	file, err := current.fs.OpenFile(current.fullPath(), os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return nfs4StatusFromErr(err)
	}
	if req.Offset > 0 {
		if _, err := file.Seek(int64(req.Offset), io.SeekStart); err != nil {
			_ = file.Close()
			return nfs4StatusFromErr(err)
		}
	}
	n, err := file.Write(req.Data)
	if err != nil {
		_ = file.Close()
		return nfs4StatusFromErr(err)
	}
	if err := file.Close(); err != nil {
		return nfs4StatusFromErr(err)
	}
	return nfs4Encode(res, nfs4WriteRes{Count: uint32(n), Committed: uint32(fileSync), Verf: c.w.Server.ID})
}
