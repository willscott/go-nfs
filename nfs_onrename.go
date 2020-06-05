package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

var doubleWccErrorBody = [16]byte{}

func onRename(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = errFormatterWithBody(doubleWccErrorBody[:])
	from := DirOpArg{}
	err := xdr.Read(w.req.Body, &from)
	if err != nil {
		return err
	}
	fs, fromPath, err := userHandle.FromHandle(from.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	to := DirOpArg{}
	err = xdr.Read(w.req.Body, &to)
	if err != nil {
		return err
	}
	fs2, toPath, err := userHandle.FromHandle(to.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}
	if fs != fs2 {
		return &NFSStatusError{NFSStatusNotSupp}
	}

	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS}
	}

	if len(string(from.Filename)) > PathNameMax || len(string(to.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong}
	}

	fromDirInfo, err := fs.Stat(fs.Join(fromPath...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt}
		}
		return &NFSStatusError{NFSStatusIO}
	}
	if !fromDirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir}
	}
	preCacheData := ToFileAttribute(fromDirInfo).AsCache()

	toDirInfo, err := fs.Stat(fs.Join(toPath...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt}
		}
		return &NFSStatusError{NFSStatusIO}
	}
	if !toDirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir}
	}
	preDestData := ToFileAttribute(toDirInfo).AsCache()

	fromLoc := fs.Join(append(fromPath, string(from.Filename))...)
	toLoc := fs.Join(append(toPath, string(to.Filename))...)

	err = fs.Rename(fromLoc, toLoc)
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

	WriteWcc(writer, preCacheData, tryStat(fs, fromPath))
	WriteWcc(writer, preDestData, tryStat(fs, toPath))

	return w.Write(writer.Bytes())
}
