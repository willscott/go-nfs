package nfs

import "io"

type nfs4GetAttrArgs struct {
	Request nfs4Bitmap
}

func nfs4OnGetAttr(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4GetAttrArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	info, err := current.fs.Lstat(current.fullPath())
	if err != nil {
		return nfs4StatusFromErr(err)
	}
	attrs, status := nfs4BuildAttrs(c.ctx, c.handler, current, info, req.Request)
	if status != nfs4OK {
		return status
	}
	return nfs4Encode(res, attrs)
}
