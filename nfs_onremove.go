package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/filesystem"
)

func onRemove(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	if err := xdr.Read(w.req.Body, &obj); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	if !filesystem.WriteCapabilityCheck(fs) {
		return &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, nil}
	}

	dirInfo, err := filesystem.Stat(fs, filesystem.Join(fs, path...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
		return &NFSStatusError{NFSStatusIO, err}
	}
	if !dirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	preCacheData := ToFileAttribute(dirInfo).AsCache()

	toDelete := filesystem.Join(fs, append(path, string(obj.Filename))...)

	err = filesystem.Remove(fs, toDelete)
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
		return &NFSStatusError{NFSStatusIO, err}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, preCacheData, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
