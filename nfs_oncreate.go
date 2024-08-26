package nfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/file"
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
		var exclusiveVerfier [8]byte
		if err := xdr.Read(w.req.Body, &exclusiveVerfier); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		attrs = verifierToDurableTimestamps(exclusiveVerfier[:])
	} else {
		// invalid
		return &NFSStatusError{NFSStatusNotSupp, os.ErrInvalid}
	}

	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, nil}
	}

	newFile := append(path, string(obj.Filename))
	newFilePath := fs.Join(newFile...)
	skipCreateAfterExclusiveMatch := false
	if s, err := fs.Stat(newFilePath); err == nil {
		if s.IsDir() {
			return &NFSStatusError{NFSStatusExist, nil}
		}
		if how == createModeGuarded {
			return &NFSStatusError{NFSStatusExist, os.ErrPermission}
		}
		if how == createModeExclusive {
			if timestampsMatch(s, attrs, fs, userHandle, newFilePath) {
				// no-op.
				skipCreateAfterExclusiveMatch = true
			} else {
				return &NFSStatusError{NFSStatusExist, os.ErrPermission}
			}
		}
	} else {
		if s, err := fs.Stat(fs.Join(path...)); err != nil {
			return &NFSStatusError{NFSStatusAccess, err}
		} else if !s.IsDir() {
			return &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	var file billy.File
	if !skipCreateAfterExclusiveMatch {
		file, err = fs.Create(newFilePath)
		if err != nil {
			Log.Errorf("Error Creating: %v", err)
			return &NFSStatusError{NFSStatusAccess, err}
		}
		if err := file.Close(); err != nil {
			Log.Errorf("Error Creating: %v", err)
			return &NFSStatusError{NFSStatusAccess, err}
		}
	} else {
		file, err = fs.Open(newFilePath)
		if err != nil {
			Log.Errorf("Error Opening(for create): %v", err)
			return &NFSStatusError{NFSStatusAccess, err}
		}
		if err := file.Close(); err != nil {
			Log.Errorf("Error Opening(for create): %v", err)
			return &NFSStatusError{NFSStatusAccess, err}
		}
	}

	fp := userHandle.ToHandle(fs, newFile)

	if !skipCreateAfterExclusiveMatch {
		changer := userHandle.Change(fs)
		if err := attrs.Apply(changer, fs, newFilePath); err != nil {
			Log.Errorf("Error applying attributes: %v\n", err)
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
	if err := WritePostOpAttrs(writer, tryStat(fs, []string{file.Name()})); err != nil {
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

func verifierToDurableTimestamps(verif []byte) *SetFileAttributes {
	if len(verif) < 8 {
		Log.Warnf("Requested file attributes from invalid verifier: %v\n", verif)
		return nil
	}
	out := SetFileAttributes{}
	mTime := binary.BigEndian.Uint32(verif[0:4])
	mTimeTime := time.Unix(int64(mTime), 0)
	out.SetMtime = &mTimeTime
	aTime := binary.BigEndian.Uint32(verif[4:8])
	aTimeTime := time.Unix(int64(aTime), 0)
	out.SetMtime = &aTimeTime

	return &out
}

func timestampsMatch(f os.FileInfo, propose *SetFileAttributes, fs billy.Filesystem, userHandle Handler, creationPath string) bool {
	// if times are equal, we can return early.
	if f.ModTime().Equal(*propose.SetMtime) && file.GetInfo(f).Atime.Equal(*propose.SetAtime) {
		return true
	}

	// otherwise, make a temp file with the proposed attributes and see if they roundtrip to what we have.
	tmpFilePath := creationPath + strconv.Itoa(rand.Int())
	tf, err := fs.Create(tmpFilePath)
	if err != nil {
		Log.Warnf("Error creating temp file for create timestamp check: %v", err)
		return false
	}
	if err := tf.Close(); err != nil {
		Log.Warnf("Error creating temp file for create timestamp check: %v", err)
		return false
	}
	changer := userHandle.Change(fs)
	if err := propose.Apply(changer, fs, tmpFilePath); err != nil {
		Log.Warnf("Error applying proposed attributes for timestamp check: %v\n", err)
		return false
	}

	// read & compare
	match := false
	if s, err := fs.Stat(tmpFilePath); err == nil {
		if s.ModTime().Equal(f.ModTime()) && file.GetInfo(s).Atime.Equal(file.GetInfo(s).Atime) {
			match = true
		}
		// Support the degraded case where atime is not roundtripped. The fallback here is that
		// the observed atime will be set equal to mtime.
		if s.ModTime().Equal(f.ModTime()) && file.GetInfo(f).Atime.Equal(f.ModTime()) {
			match = true
		}
	} else {
		Log.Warnf("Unable to stat temp file for create timestamp check: %v", err)
	}

	if err := fs.Remove(tmpFilePath); err != nil {
		Log.Warnf("Error cleaning up temp file for create timestamp check: %v", err)
	}

	return match
}
