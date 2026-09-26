package nfs

import "io"

type nfs4ClientAddr struct {
	NetID string
	Addr  string
}

type nfs4SetClientIDArgs struct {
	Verifier      [8]byte
	ID            []byte
	CallbackProg  uint32
	CallbackAddr  nfs4ClientAddr
	CallbackIdent uint32
}

type nfs4SetClientIDRes struct {
	ClientID uint64
	Confirm  [8]byte
}

// nfs4OnSetClientID hands out a client ID, which the client confirms with
// SETCLIENTID_CONFIRM. Callbacks are never used: this server hands out no
// delegations.
func nfs4OnSetClientID(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4SetClientIDArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if len(req.ID) > nfs4OpaqueLimit {
		return nfs4ErrBadXDR
	}
	var out nfs4SetClientIDRes
	out.ClientID, out.Confirm = c.w.Server.nfs4State().setClientID(req.ID, req.Verifier)
	return nfs4Encode(res, out)
}
