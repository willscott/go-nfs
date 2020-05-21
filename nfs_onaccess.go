package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

func onAccess(ctx context.Context, w *response, userHandle Handler) error {
	roothandle, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(roothandle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}
	mask, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, path)

	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		mask = mask & (1 | 2 | 0x20)
	}

	if err := xdr.Write(writer, mask); err != nil {
		return err
	}
	return w.Write(writer.Bytes())
}
