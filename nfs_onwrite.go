package nfs

import (
	"bytes"
	"context"
	"io"
	"log"
	"math"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
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
	Offset uint64
	Count  uint32
	How    uint32
	Data   []byte
}

func onWrite(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	var req writeArgs
	if err := xdr.Read(w.req.Body, &req); err != nil {
		return err
	}

	fs, path, err := userHandle.FromHandle(req.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS}
	}
	if len(req.Data) > math.MaxInt32 || req.Count > math.MaxInt32 {
		return &NFSStatusError{NFSStatusFBig}
	}

	// stat first for pre-op wcc.
	info, err := fs.Stat(fs.Join(path...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt}
		}
		return &NFSStatusError{NFSStatusAccess}
	}
	if !info.Mode().IsRegular() {
		return &NFSStatusError{NFSStatusInval}
	}
	preOpCache := ToFileAttribute(info).AsCache()

	// now the actual op.
	file, err := fs.OpenFile(fs.Join(path...), os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return &NFSStatusError{NFSStatusAccess}
	}
	if req.Offset > 0 {
		if _, err := file.Seek(int64(req.Offset), io.SeekStart); err != nil {
			return &NFSStatusError{NFSStatusIO}
		}
	}
	end := req.Count
	if len(req.Data) < int(end) {
		end = uint32(len(req.Data))
	}
	writtenCount, err := file.Write(req.Data[:end])
	if err != nil {
		log.Printf("Error writing: %v", err)
		return &NFSStatusError{NFSStatusIO}
	}
	if err := file.Close(); err != nil {
		log.Printf("error closing: %v", err)
		return &NFSStatusError{NFSStatusIO}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}

	WriteWcc(writer, preOpCache, tryStat(fs, path))
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
