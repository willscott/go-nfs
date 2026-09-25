package nfs

import "io"

type nfs4SetAttrArgs struct {
	StateID nfs4StateID
	Attrs   nfs4FAttr
}

// nfs4OnSetAttr answers with the attributes it set, an empty set when it
// fails: SETATTR4res carries attrsset whatever the status.
func nfs4OnSetAttr(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	set, status := nfs4SetAttr(c, args)
	if status != nfs4OK {
		set = nil
	}
	if encoded := nfs4Encode(res, set); status == nfs4OK {
		status = encoded
	}
	return status
}

func nfs4SetAttr(c *nfs4Compound, args io.Reader) (nfs4Bitmap, nfs4Status) {
	var req nfs4SetAttrArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return nil, status
	}
	attrs, status := nfs4DecodeSetAttrs(req.Attrs)
	if status != nfs4OK {
		return nil, status
	}
	current, status := c.requireCurrent()
	if status != nfs4OK {
		return nil, status
	}
	c.w.Server.nfs4State().renewState(req.StateID)
	if err := attrs.attrs.Apply(c.handler.Change(current.fs), current.fs, current.fullPath()); err != nil {
		return nil, nfs4StatusFromErr(err)
	}
	return attrs.mask, nfs4OK
}
