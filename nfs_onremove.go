package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

func onRemove(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong}
	}

	dirInfo, err := fs.Stat(fs.Join(path...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt}
		}
		return &NFSStatusError{NFSStatusIO}
	}
	if !dirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir}
	}
	preCacheData := ToFileAttribute(dirInfo).AsCache()

	toDelete := fs.Join(append(path, string(obj.Filename))...)

	err = fs.Remove(toDelete)
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess}
		}
		return &NFSStatusError{NFSStatusIO}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}

	WriteWcc(writer, preCacheData, tryStat(fs, path))

	return w.Write(writer.Bytes())
}
