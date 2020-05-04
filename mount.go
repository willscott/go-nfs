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
}

func onMount(ctx context.Context, w *response, userHandle Handler) error {
	// TODO: auth check.
	dirpath, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}
	mountReq := MountRequest{Header: w.req.Header, Dirpath: dirpath}
	status, handle, flavors := userHandle.Mount(ctx, w.conn, mountReq)

	// Override RPC header
	// TODO: revisit - this ugliness is probably indicative that the conn interface needs work.
	w.responded = true
	if err := w.writeXdrHeader(); err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	xdr.Write(writer, uint32(status))
	if status == MountStatusOk {
		xdr.Write(writer, handle)
		xdr.Write(writer, flavors)
	}
	return w.Write(writer.Bytes())
}
