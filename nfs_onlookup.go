package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

func lookupSuccessResponse(handle []byte, entPath, dirPath []string, fs billy.Filesystem) []byte {
	writer := bytes.NewBuffer([]byte{})
	xdr.Write(writer, uint32(NFSStatusOk))
	xdr.Write(writer, handle)
	WritePostOpAttrs(writer, fs, entPath)
	WritePostOpAttrs(writer, fs, dirPath)
	return writer.Bytes()
}

func onLookup(ctx context.Context, w *response, userHandle Handler) error {
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}

	fs, p, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusErrorWithOpAttr{NFSStatusStale}
	}
	contents, err := fs.ReadDir(fs.Join(p...))
	if err != nil {
		return &NFSStatusErrorWithOpAttr{NFSStatusNotDir}
	}

	// Special cases for "." and ".."
	if bytes.Equal(obj.Filename, []byte(".")) {
		return w.Write(lookupSuccessResponse(obj.Handle, p, p, fs))
	}
	if bytes.Equal(obj.Filename, []byte("..")) {
		if len(p) == 0 {
			return &NFSStatusErrorWithOpAttr{NFSStatusAccess}
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

	return &NFSStatusErrorWithOpAttr{NFSStatusNoEnt}
}
