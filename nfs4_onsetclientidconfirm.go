package nfs

import "io"

type nfs4SetClientIDConfirmArgs struct {
	ClientID uint64
	Confirm  [8]byte
}

func nfs4OnSetClientIDConfirm(_ *nfs4Compound, args io.Reader, _ io.Writer) nfs4Status {
	var req nfs4SetClientIDConfirmArgs
	return nfs4Decode(args, &req)
}
