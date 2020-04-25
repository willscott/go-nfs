package nfs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"

	"github.com/vmware/go-nfs-client/nfs/rpc"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

var (
	// ErrInputInvalid is returned when input cannot be parsed
	ErrInputInvalid = errors.New("invalid input")
	// ErrAlreadySent is returned when writing a header/status multiple times
	ErrAlreadySent = errors.New("response already started")
)

// ResponseCode is a combination of accept_stat and reject_stat.
type ResponseCode uint32

// ResponseCode Codes
const (
	ResponseCodeSuccess ResponseCode = iota
	ResponseCodeProgUnavailable
	ResponseCodeProcUnavailable
	ResponseCodeGarbageArgs
	ResponseCodeSystemErr
	ResponseCodeRPCMismatch
	ResponseCodeAuthError
)

type conn struct {
	*Server
	writeSerializer chan []byte
	net.Conn
}

func (c *conn) serve(ctx context.Context) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.writeSerializer = make(chan []byte, 1)
	go c.serializeWrites(connCtx)

	bio := bufio.NewReader(c.Conn)
	for {
		w, err := c.readRequestHeader(connCtx, bio)
		if err != nil {
			return
		}
		go c.handle(connCtx, w)
	}
}

func (c *conn) serializeWrites(ctx context.Context) {
	writer := bufio.NewWriter(c.Conn)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.writeSerializer:
			if !ok {
				return
			}
			n, err := writer.Write(msg)
			if err != nil {
				return
			}
			if n < len(msg) {
				panic("todo: ensure writes complete fully.")
			}
		}
	}
}

func (c *conn) handle(ctx context.Context, w *response) {
	handler := c.Server.handlerFor(w.req.Header.Prog, w.req.Header.Proc)
	if handler == nil {
		c.err(ctx, w, &ResponseCodeProcUnavailableError{})
		return
	}
	err := handler(ctx, w, c.Server.Handler)
	if err != nil && !w.responded {
		c.err(ctx, w, err)
	}
	return
}

func (c *conn) err(ctx context.Context, w *response, err error) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	if w.err == nil {
		w.err = err
	}

	if w.responded {
		return
	}

	var rpcErr RPCError
	if errors.As(err, &rpcErr) {
		w.writeHeader(rpcErr.Code())
		body, _ := rpcErr.MarshalBinary()
		w.write(body)
	} else {
		w.writeHeader(ResponseCodeSystemErr)
	}
	w.finish()
}

type request struct {
	xid uint32
	rpc.Header
	Body *bufio.Reader
}

type response struct {
	*conn
	writer    *bytes.Buffer
	responded bool
	err       error
	req       *request
}

func (w *response) writeXdrHeader() error {
	err := xdr.Write(w.writer, &w.req.xid)
	if err != nil {
		return err
	}
	respType := uint32(1)
	err = xdr.Write(w.writer, &respType)
	if err != nil {
		return err
	}
	return nil
}

func (w *response) writeHeader(code ResponseCode) error {
	if w.responded {
		return ErrAlreadySent
	}
	w.responded = true
	if err := w.writeXdrHeader(); err != nil {
		return err
	}

	status := rpc.MsgAccepted
	if code == ResponseCodeAuthError || code == ResponseCodeRPCMismatch {
		status = rpc.MsgDenied
	}

	err := xdr.Write(w.writer, &status)
	if err != nil {
		return err
	}

	if status == rpc.MsgAccepted {
		// Write opaque_auth header.
	}

	return nil
}

// Write a response to an xdr message
func (w *response) write(dat []byte) error {
	if !w.responded {
		w.writeHeader(ResponseCodeSuccess)
	}

	acc := 0
	for acc < len(dat) {
		n, err := w.writer.Write(dat[acc:])
		if err != nil {
			return err
		}
		acc += n
	}
	return nil
}

func (w *response) finish() error {
	w.conn.writeSerializer <- w.writer.Bytes()
	return nil
}

func (c *conn) readRequestHeader(ctx context.Context, reader *bufio.Reader) (w *response, err error) {
	xid, err := xdr.ReadUint32(reader)
	if err != nil {
		return nil, err
	}
	reqType, err := xdr.ReadUint32(reader)
	if err != nil {
		return nil, err
	}
	if reqType != 0 { // 0 = request, 1 = response
		return nil, ErrInputInvalid
	}
	hdr := rpc.Header{}
	if err = xdr.Read(reader, &hdr); err != nil {
		return nil, err
	}

	req := request{
		xid,
		hdr,
		reader,
	}

	w = &response{
		conn: c,
		req:  &req,
		// TODO: use a pool for these.
		writer: bytes.NewBuffer([]byte{}),
	}
	return w, nil
}
