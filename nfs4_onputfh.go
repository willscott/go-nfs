package nfs

import "io"

type nfs4PutFHArgs struct {
	Handle []byte
}

func nfs4OnPutFH(c *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4PutFHArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if len(req.Handle) > nfs4FhSize {
		return nfs4ErrBadHandle
	}
	fs, p, err := c.handler.FromHandle(req.Handle)
	if err != nil {
		return nfs4ErrStale
	}
	c.current = &nfs4FileHandle{handle: req.Handle, fs: fs, path: nfs4CopyPath(p)}
	return nfs4OK
}
