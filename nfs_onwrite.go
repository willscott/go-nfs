package nfs

import (
	"bytes"
	"context"
	"math"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

// writeStability is the level of durability requested with the write
type writeStability uint32

const (
	unstable writeStability = 0
	dataSync writeStability = 1
	fileSync writeStability = 2
)

type writeArgs struct {
	Handle []byte
	Offset uint32
	Count  uint32
	How    uint32
	Data   []byte
}

func onWrite(ctx context.Context, w *response, userHandle Handler) error {
	var req writeArgs
	if err := xdr.Read(w.req.Body, req); err != nil {
		return err
	}

	fs, path, err := userHandle.FromHandle(req.Handle)
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusStale}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusErrorWithWccData{NFSStatusROFS}
	}
	if len(req.Data) > math.MaxInt32 || req.Count > math.MaxInt32 {
		return &NFSStatusErrorWithWccData{NFSStatusFBig}
	}

	// stat first for pre-op wcc.
	info, err := fs.Stat(fs.Join(path...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusErrorWithWccData{NFSStatusNoEnt}
		}
		return &NFSStatusErrorWithWccData{NFSStatusAccess}
	}
	if !info.Mode().IsRegular() {
		return &NFSStatusErrorWithWccData{NFSStatusInval}
	}
	preOpCache := ToFileAttribute(info).AsCache()

	// now the actual op.
	file, err := fs.Open(fs.Join(path...))
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusAccess}
	}
	if _, err := file.Seek(int64(req.Offset), 0); err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusIO}
	}
	end := req.Count
	if len(req.Data) < int(end) {
		end = uint32(len(req.Data))
	}
	writtenCount, err := file.Write(req.Data[:end])
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusIO}
	}
	if err := file.Close(); err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusIO}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}

	if err := xdr.Write(writer, uint32(1)); err != nil {
		return err
	}
	if err := xdr.Write(writer, *preOpCache); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, append(path, file.Name()))
	if err := xdr.Write(writer, uint32(writtenCount)); err != nil {
		return err
	}
	if err := xdr.Write(writer, fileSync); err != nil {
		return err
	}
	if err := xdr.Write(writer, w.Server.ID); err != nil {
		return err
	}

	return w.Write(writer.Bytes())
}
