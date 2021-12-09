package nfs

import (
	"bytes"
	"context"
	"log"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/filesystem"
)

const (
	createModeUnchecked = 0
	createModeGuarded   = 1
	createModeExclusive = 2
)

func onCreate(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	how, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	var attrs *SetFileAttributes
	if how == createModeUnchecked || how == createModeGuarded {
		sattr, err := ReadSetFileAttributes(w.req.Body)
		if err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		attrs = sattr
	} else if how == createModeExclusive {
		// read createverf3
		var verf [8]byte
		if err := xdr.Read(w.req.Body, &verf); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		log.Printf("failing create to indicate lack of support for 'exclusive' mode.")
		// TODO: support 'exclusive' mode.
		return &NFSStatusError{NFSStatusNotSupp, os.ErrPermission}
	} else {
		// invalid
		return &NFSStatusError{NFSStatusNotSupp, os.ErrInvalid}
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

	newFilePath := filesystem.Join(fs, append(path, string(obj.Filename))...)
	if s, err := filesystem.Stat(fs, newFilePath); err == nil {
		if s.IsDir() {
			return &NFSStatusError{NFSStatusExist, nil}
		}
		if how == createModeGuarded {
			return &NFSStatusError{NFSStatusExist, os.ErrPermission}
		}
	} else {
		if s, err := filesystem.Stat(fs, filesystem.Join(fs, path...)); err != nil {
			return &NFSStatusError{NFSStatusAccess, err}
		} else if !s.IsDir() {
			return &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	file, err := filesystem.Create(fs, newFilePath)
	if err != nil {
		log.Printf("Error Creating: %v", err)
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if err := file.Close(); err != nil {
		log.Printf("Error Creating: %v", err)
		return &NFSStatusError{NFSStatusAccess, err}
	}

	fileStats, err := file.Stat()
	if err != nil {
		log.Printf("Error reading stats: %v", err)
		return &NFSStatusError{NFSStatusAccess, err}
	}

	fp := userHandle.ToHandle(fs, append(path, fileStats.Name()))
	changer := userHandle.Change(fs)
	if err := attrs.Apply(changer, fs, newFilePath); err != nil {
		log.Printf("Error applying attributes: %v\n", err)
		return &NFSStatusError{NFSStatusIO, err}
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
	if err := WritePostOpAttrs(writer, tryStat(fs, append(path, fileStats.Name()))); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	// dir_wcc (we don't include pre_op_attr)
	if err := xdr.Write(writer, uint32(0)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
