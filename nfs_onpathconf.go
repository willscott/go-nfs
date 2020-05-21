package nfs

import (
	"bytes"
	"context"

	"github.com/vmware/go-nfs-client/nfs/xdr"
)

func onPathConf(ctx context.Context, w *response, userHandle Handler) error {
	roothandle, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(roothandle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, path)

	type PathConf struct {
		LinkMax         uint32
		NameMax         uint32
		NoTrunc         uint32
		ChownRestricted uint32
		CaseInsensitive uint32
		CasePreserving  uint32
	}

	defaults := PathConf{
		LinkMax:         1,
		NameMax:         255,
		NoTrunc:         1,
		ChownRestricted: 0,
		CaseInsensitive: 0,
		CasePreserving:  1,
	}
	if err := xdr.Write(writer, defaults); err != nil {
		return err
	}
	return w.Write(writer.Bytes())
}
