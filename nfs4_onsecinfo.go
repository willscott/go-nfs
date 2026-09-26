package nfs

import "io"

type nfs4SecInfoArgs struct {
	Name string
}

// nfs4OnSecInfo offers AUTH_SYS and AUTH_NONE. A secinfo4 for either is its
// flavor alone.
func nfs4OnSecInfo(_ *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4SecInfoArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if status := nfs4Component(req.Name); status != nfs4OK {
		return status
	}
	return nfs4Encode(res, []AuthFlavor{AuthFlavorUnix, AuthFlavorNull})
}
