package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onReadLink(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = opAttrErrorFormatter
	handle, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	out, err := fs.Readlink(fs.Join(path...))
	if err != nil {
		if info, err := fs.Stat(fs.Join(path...)); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return &NFSStatusError{NFSStatusInval}
			}
		}
		// err
		return &NFSStatusError{NFSStatusAccess}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, tryStat(fs, path))

	if err := xdr.Write(writer, out); err != nil {
		return err
	}
	return w.Write(writer.Bytes())
}
