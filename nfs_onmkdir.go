package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

const (
	mkdirDefaultMode = 755
)

func onMkdir(ctx context.Context, w *response, userHandle Handler) error {
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}

	attrs, err := ReadSetFileAttributes(w.req.Body)
	if err != nil {
		return err
	}

	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusStale}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusErrorWithWccData{NFSStatusROFS}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusErrorWithWccData{NFSStatusNameTooLong}
	}
	if string(obj.Filename) == "." || string(obj.Filename) == ".." {
		return &NFSStatusErrorWithWccData{NFSStatusExist}
	}

	newFolder := append(path, string(obj.Filename))
	newFolderPath := fs.Join(newFolder...)
	if s, err := fs.Stat(newFolderPath); err == nil {
		if s.IsDir() {
			return &NFSStatusErrorWithWccData{NFSStatusExist}
		}
	} else {
		if s, err := fs.Stat(fs.Join(path...)); err != nil {
			return &NFSStatusErrorWithWccData{NFSStatusAccess}
		} else if !s.IsDir() {
			return &NFSStatusErrorWithWccData{NFSStatusNotDir}
		}
	}

	if err := fs.MkdirAll(newFolderPath, attrs.Mode(mkdirDefaultMode)); err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusAccess}
	}

	fp := userHandle.ToHandle(fs, newFolder)
	changer := userHandle.Change(fs)
	if changer != nil {
		if err := attrs.Apply(changer, fs, newFolderPath); err != nil {
			return &NFSStatusErrorWithWccData{NFSStatusIO}
		}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}

	// "handle follows"
	if err := xdr.Write(writer, uint32(1)); err != nil {
		return err
	}
	if err := xdr.Write(writer, fp); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, newFolder)

	// dir_wcc (we don't include pre_op_attr)
	if err := xdr.Write(writer, uint32(0)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, path)

	return w.Write(writer.Bytes())
}
