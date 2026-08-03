package nfs

import (
	"bytes"
	"context"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

const (
	mountServiceID = 100005
)

func init() {
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcNull), onMountNull)
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcMount), onMount)
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcUmnt), onUMount)
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcExport), onExport)
}

func onMountNull(ctx context.Context, w *response, userHandle Handler) error {
	return w.writeHeader(ResponseCodeSuccess)
}

func onMount(ctx context.Context, w *response, userHandle Handler) error {
	// TODO: auth check.
	dirpath, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}
	mountReq := MountRequest{Header: w.req.Header, Dirpath: dirpath}
	status, handle, flavors := userHandle.Mount(ctx, w.conn, mountReq)

	if err := w.writeHeader(ResponseCodeSuccess); err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(status)); err != nil {
		return err
	}

	rootHndl := userHandle.ToHandle(handle, []string{})

	if status == MountStatusOk {
		_ = xdr.Write(writer, rootHndl)
		_ = xdr.Write(writer, flavors)
	}
	return w.Write(writer.Bytes())
}

func onUMount(ctx context.Context, w *response, userHandle Handler) error {
	_, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}

	return w.writeHeader(ResponseCodeSuccess)
}

// onExport implements MOUNTPROC_EXPORT (procedure 5) of the MOUNTv3 protocol,
// returning the list of exports the server advertises. This is the call made
// by `showmount -e` and many NFS storage-discovery probes (Hammerspace Anvil,
// monitoring scripts, automounter discovery). Without it the dispatcher
// writes no reply and the client times out.
//
// If userHandle implements ExportHandler, its Exports() method provides the
// list; otherwise a single entry advertising "/" with no group restriction is
// returned, which matches what a Handler effectively serves (the Mount RPC
// hands out the same root handle regardless of the requested dirpath).
//
// See RFC 1813 section 5.2.5 for the wire format.
func onExport(ctx context.Context, w *response, userHandle Handler) error {
	var exports []Export
	if eh, ok := userHandle.(ExportHandler); ok {
		exports = eh.Exports()
	}
	if len(exports) == 0 {
		exports = []Export{{Dir: "/"}}
	}

	if err := w.writeHeader(ResponseCodeSuccess); err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})

	for _, e := range exports {
		// value-follows boolean for this export entry
		if err := xdr.Write(writer, uint32(1)); err != nil {
			return err
		}
		// ex_dir
		if err := xdr.Write(writer, []byte(e.Dir)); err != nil {
			return err
		}
		// ex_groups - linked list of group name strings
		for _, g := range e.Groups {
			if err := xdr.Write(writer, uint32(1)); err != nil {
				return err
			}
			if err := xdr.Write(writer, []byte(g)); err != nil {
				return err
			}
		}
		// terminator for ex_groups
		if err := xdr.Write(writer, uint32(0)); err != nil {
			return err
		}
	}
	// terminator for ex_next (end of exports list)
	if err := xdr.Write(writer, uint32(0)); err != nil {
		return err
	}

	return w.Write(writer.Bytes())
}
