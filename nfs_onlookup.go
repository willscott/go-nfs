package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func lookupSuccessResponse(handle []byte, entPath, dirPath []string, fs billy.Filesystem) []byte {
	writer := bytes.NewBuffer([]byte{})
	_ = xdr.Write(writer, uint32(NFSStatusOk))
	_ = xdr.Write(writer, handle)
	WritePostOpAttrs(writer, tryStat(fs, entPath))
	WritePostOpAttrs(writer, tryStat(fs, dirPath))
	return writer.Bytes()
}

func onLookup(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = opAttrErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}

	fs, p, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}
	contents, err := fs.ReadDir(fs.Join(p...))
	if err != nil {
		return &NFSStatusError{NFSStatusNotDir}
	}

	// Special cases for "." and ".."
	if bytes.Equal(obj.Filename, []byte(".")) {
		return w.Write(lookupSuccessResponse(obj.Handle, p, p, fs))
	}
	if bytes.Equal(obj.Filename, []byte("..")) {
		if len(p) == 0 {
			return &NFSStatusError{NFSStatusAccess}
		}
		pPath := p[0 : len(p)-1]
		pHandle := userHandle.ToHandle(fs, pPath)
		return w.Write(lookupSuccessResponse(pHandle, pPath, p, fs))
	}

	// TODO: use sorting rather than linear
	for _, f := range contents {
		if bytes.Equal([]byte(f.Name()), obj.Filename) {
			newPath := append(p, f.Name())
			newHandle := userHandle.ToHandle(fs, newPath)
			return w.Write(lookupSuccessResponse(newHandle, newPath, p, fs))
		}
	}

	return &NFSStatusError{NFSStatusNoEnt}
}
