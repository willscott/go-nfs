package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/filesystem"
)

const (
	mkdirDefaultMode = 755
)

func onMkdir(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	attrs, err := ReadSetFileAttributes(w.req.Body)
	if err != nil {
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
		return &NFSStatusError{NFSStatusNameTooLong, os.ErrInvalid}
	}
	if string(obj.Filename) == "." || string(obj.Filename) == ".." {
		return &NFSStatusError{NFSStatusExist, os.ErrExist}
	}

	newFolder := append(path, string(obj.Filename))
	newFolderPath := filesystem.Join(fs, newFolder...)
	if s, err := filesystem.Stat(fs, newFolderPath); err == nil {
		if s.IsDir() {
			return &NFSStatusError{NFSStatusExist, nil}
		}
	} else {
		if s, err := filesystem.Stat(fs, filesystem.Join(fs, path...)); err != nil {
			return &NFSStatusError{NFSStatusAccess, err}
		} else if !s.IsDir() {
			return &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	if err := filesystem.MkdirAll(fs, newFolderPath, attrs.Mode(mkdirDefaultMode)); err != nil {
		return &NFSStatusError{NFSStatusAccess, err}
	}

	fp := userHandle.ToHandle(fs, newFolder)
	changer := userHandle.Change(fs)
	if changer != nil {
		if err := attrs.Apply(changer, fs, newFolderPath); err != nil {
			return &NFSStatusError{NFSStatusIO, err}
		}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	// "handle follows"
	if err := xdr.Write(writer, uint32(1)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, fp); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, tryStat(fs, newFolder)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, nil, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
