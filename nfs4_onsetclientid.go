package nfs

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
)

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

// nfs4OnSetClientID derives the client ID from the client's verifier and
// name, so a client gets the same one back until it reboots. Callbacks are
// never used: this server hands out no delegations.
func nfs4OnSetClientID(_ *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4SetClientIDArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if len(req.ID) > nfs4OpaqueLimit {
		return nfs4ErrBadXDR
	}
	sum := sha256.Sum256(append(req.Verifier[:], req.ID...))
	out := nfs4SetClientIDRes{ClientID: binary.BigEndian.Uint64(sum[:8])}
	copy(out.Confirm[:], sum[8:16])
	return nfs4Encode(res, out)
}
