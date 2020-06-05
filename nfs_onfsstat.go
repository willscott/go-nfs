package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onFSStat(ctx context.Context, w *response, userHandle Handler) error {
	roothandle, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(roothandle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	defaults := FSStat{
		TotalSize:      1 << 62,
		FreeSize:       1 << 62,
		AvailableSize:  1 << 62,
		TotalFiles:     1 << 62,
		FreeFiles:      1 << 62,
		AvailableFiles: 1 << 62,
		CacheHint:      0,
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		defaults.AvailableFiles = 0
		defaults.AvailableSize = 0
	}

	err = userHandle.FSStat(ctx, fs, &defaults)
	if err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, tryStat(fs, path))

	if err := xdr.Write(writer, defaults); err != nil {
		return err
	}
	return w.Write(writer.Bytes())
}
