package nfs

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/vmware/go-nfs-client/nfs/xdr"
)

type nfsReadArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
}

type nfsReadResponse struct {
	Count uint32
	Eof   uint32
	Data  []byte
}

const MaxRead = 1 << 24
const CheckRead = 1 << 15

func onRead(ctx context.Context, w *response, userHandle Handler) error {
	var obj nfsReadArgs
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}
	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusErrorWithOpAttr{NFSStatusStale}
	}

	fh, err := fs.Open(fs.Join(path...))
	if err != nil {
		// err
		return &NFSStatusErrorWithOpAttr{NFSStatusAccess}
	}

	resp := nfsReadResponse{}

	if obj.Count > CheckRead {
		info, err := fs.Stat(fs.Join(path...))
		if err != nil {
			return &NFSStatusErrorWithOpAttr{NFSStatusAccess}
		}
		if info.Size()-int64(obj.Offset) < int64(obj.Count) {
			obj.Count = uint32(uint64(info.Size()) - obj.Offset)
		}
	}
	if obj.Count > MaxRead {
		obj.Count = MaxRead
	}
	resp.Data = make([]byte, obj.Count)
	// todo: multiple reads if size isn't full
	cnt, err := fh.ReadAt(resp.Data, int64(obj.Offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return &NFSStatusErrorWithOpAttr{NFSStatusIO}
	}
	resp.Count = uint32(cnt)
	resp.Data = resp.Data[:resp.Count]
	if errors.Is(err, io.EOF) {
		resp.Eof = 1
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, path)

	if err := xdr.Write(writer, resp); err != nil {
		return err
	}
	return w.Write(writer.Bytes())
}
