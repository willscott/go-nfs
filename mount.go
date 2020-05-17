package nfs

import (
	"bytes"
	"context"

	"github.com/vmware/go-nfs-client/nfs/xdr"
)

const (
	mountServiceID = 100005
)

func init() {
	RegisterMessageHandler(mountServiceID, uint32(MountProcMount), onMount)
	RegisterMessageHandler(mountServiceID, uint32(MountProcUmnt), onUMount)
}

func onMount(ctx context.Context, w *response, userHandle Handler) error {
	// TODO: auth check.
	dirpath, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}
	mountReq := MountRequest{Header: w.req.Header, Dirpath: dirpath}
	status, handle, flavors := userHandle.Mount(ctx, w.conn, mountReq)

	w.writeHeader(ResponseCodeSuccess)

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(status)); err != nil {
		return err
	}

	rootHndl := userHandle.ToHandle(handle, []string{})

	if status == MountStatusOk {
		xdr.Write(writer, rootHndl)
		xdr.Write(writer, flavors)
	}
	return w.Write(writer.Bytes())
}

func onUMount(ctx context.Context, w *response, userHandle Handler) error {
	_, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}

	w.writeHeader(ResponseCodeSuccess)
	return nil
}
