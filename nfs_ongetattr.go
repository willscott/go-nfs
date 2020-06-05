package nfs

import (
	"bytes"
	"context"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onGetAttr(ctx context.Context, w *response, userHandle Handler) error {
	handle, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}

	fs, path, err := userHandle.FromHandle(handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	info, err := fs.Stat(fs.Join(path...))
	if err != nil {
		// TODO: wrap
		return err
	}
	attr := ToFileAttribute(info)

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	if err := xdr.Write(writer, attr); err != nil {
		return err
	}

	return w.Write(writer.Bytes())
}
