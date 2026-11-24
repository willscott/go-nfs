package nfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	xdr2 "github.com/rasky/go-xdr/xdr2"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"
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
	ResponseCodeProgMismatch
	ResponseCodeProcUnavailable
	ResponseCodeGarbageArgs
	ResponseCodeSystemErr
	ResponseCodeRPCMismatch
	ResponseCodeAuthError
)

type conn struct {
	*Server
	net.Conn
	writeSerializer chan []byte
	// cancel ends the connection: it cancels the context its requests are
	// handled with, which closes the network connection.
	cancel context.CancelFunc

	// idleMu guards idle, which is true while the connection waits for its
	// next request, so Shutdown can interrupt the read without cutting off
	// a request.
	idleMu sync.Mutex
	idle   bool
}

// Close closes the network connection. Server and net.Conn both have a
// Close method; this one keeps conn a net.Conn for Handler.Mount.
func (c *conn) Close() error {
	return c.Conn.Close()
}

// maxRequestFragmentBytes rejects implausible record-marking lengths before a
// request body is buffered for concurrent handling. Linux caps rsize and
// wsize at 1 MiB, so real requests stay well under this.
const maxRequestFragmentBytes = 16 << 20

// RFC 5531 section 8.2 limits each opaque_auth body to 400 bytes.
const maxAuthBytes = 400

// serve handles requests from the connection until it closes, the server
// shuts down, or ctx ends.
//
// When the client closes the connection or the server shuts down
// gracefully, the requests already read are still answered before the
// connection is closed. Any other failure cancels them and closes the
// connection at once. Either way, serve returns only after every goroutine
// it started has finished.
func (c *conn) serve(ctx context.Context) {
	defer c.cancel()
	// Closing the connection is what unblocks reads and writes, so do it as
	// soon as ctx ends.
	closeConn := sync.OnceFunc(func() { _ = c.Conn.Close() })
	stop := context.AfterFunc(ctx, closeConn)
	defer func() {
		stop()
		closeConn()
	}()

	limit := max(c.Server.ConcurrentHandlers, 1)
	c.writeSerializer = make(chan []byte, limit)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.serializeWrites()
	}()

	var handlers sync.WaitGroup
	var graceful bool
	if limit > 1 {
		graceful = c.readConcurrently(ctx, &handlers, limit)
	} else {
		graceful = c.readSequentially(ctx)
	}
	if !graceful {
		c.cancel()
	}
	handlers.Wait()
	// Nothing sends to writeSerializer any more; let the writer flush what
	// is queued and stop.
	close(c.writeSerializer)
	<-writerDone
}

// readSequentially handles requests one at a time, in order. It reports
// whether the connection ended gracefully, so replies already queued
// should still be sent.
func (c *conn) readSequentially(ctx context.Context) bool {
	bio := bufio.NewReader(c.Conn)
	for {
		w, ok, graceful := c.nextRequest(ctx, bio)
		if !ok {
			return graceful
		}
		err := c.handle(ctx, w)
		respErr := w.finish(ctx)
		if err != nil {
			Log.Errorf("error handling req: %v", err)
			// failure to handle at a level needing to close the connection.
			return false
		}
		if respErr != nil {
			if respErr != context.Canceled {
				Log.Errorf("error sending response: %v", respErr)
			}
			return false
		}
	}
}

// readConcurrently handles up to limit requests from the connection at once.
// Linux clients multiplex every process's I/O on a mount over one TCP
// connection, so handling requests one at a time lets a single slow
// operation stall the whole mount. RPC replies carry the request's XID, so
// completing them out of order is legal.
//
// Each request body is read into memory before its handler starts, so the
// loop can read the next request while earlier ones are still running.
// Handlers run in handlers; the caller waits for them.
func (c *conn) readConcurrently(ctx context.Context, handlers *sync.WaitGroup, limit int) bool {
	sem := make(chan struct{}, limit)
	bio := bufio.NewReader(c.Conn)
	for {
		w, ok, graceful := c.nextRequest(ctx, bio)
		if !ok {
			return graceful
		}
		if err := w.bufferBody(); err != nil {
			Log.Errorf("error reading request body: %v", err)
			return false
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return false
		}
		handlers.Go(func() {
			defer func() { <-sem }()

			err := c.handle(ctx, w)
			respErr := w.finish(ctx)
			if err != nil {
				Log.Errorf("error handling req: %v", err)
				// failure to handle at a level needing to close the connection.
				c.cancel()
				return
			}
			if respErr != nil && respErr != context.Canceled {
				Log.Errorf("error sending response: %v", respErr)
				c.cancel()
			}
		})
	}
}

// nextRequest reads the header of the next request. When there is none, ok
// is false, and graceful reports whether the connection ended cleanly: the
// client closed it, or the server is shutting down.
func (c *conn) nextRequest(ctx context.Context, bio *bufio.Reader) (w *response, ok, graceful bool) {
	if !c.setIdle(true) {
		return nil, false, true
	}
	w, err := c.readRequestHeader(ctx, bio)
	c.setIdle(false)
	if err != nil {
		// A clean close, or a request that can't be framed: either way
		// nothing more can be read from this connection.
		return nil, false, err == io.EOF || c.Server.inShutdown.Load()
	}
	Log.Tracef("request: %v", w.req)
	return w, true, false
}

// setIdle marks whether the connection is waiting for its next request.
// Marking it idle fails once the server is shutting down: the connection
// should take no new requests.
func (c *conn) setIdle(idle bool) bool {
	c.idleMu.Lock()
	defer c.idleMu.Unlock()
	if idle && c.Server.inShutdown.Load() {
		return false
	}
	c.idle = idle
	if !idle && c.Server.inShutdown.Load() {
		// Shutdown may have interrupted the read just as a request arrived.
		// Let the rest of it be read.
		_ = c.Conn.SetReadDeadline(time.Time{})
	}
	return true
}

// interruptIdleRead ends the wait for the next request if the connection is
// waiting for one, for Shutdown. A connection handling a request is left to
// finish it; it then sees the shutdown and takes no more.
func (c *conn) interruptIdleRead() {
	c.idleMu.Lock()
	defer c.idleMu.Unlock()
	if c.idle {
		// A deadline in the past fails the pending read without closing
		// the connection, so queued replies can still be written.
		_ = c.Conn.SetReadDeadline(time.Unix(1, 0))
	}
}

// serializeWrites writes replies to the connection in the order they are
// queued, until writeSerializer is closed. If a write fails, the
// connection is ended.
func (c *conn) serializeWrites() {
	writer := bufio.NewWriter(c.Conn)
	var fragmentBuf [4]byte
	for msg := range c.writeSerializer {
		// prepend the fragmentation header
		binary.BigEndian.PutUint32(fragmentBuf[:], uint32(len(msg))|1<<31)
		if _, err := writer.Write(fragmentBuf[:]); err != nil {
			break
		}
		if _, err := writer.Write(msg); err != nil {
			break
		}
		// Flush once no more replies are waiting, so a burst goes out in
		// fewer writes.
		if len(c.writeSerializer) > 0 {
			continue
		}
		if err := writer.Flush(); err != nil {
			break
		}
	}
	// Either writeSerializer was closed and the connection is ending, or a
	// write failed and it must: no more replies can be sent.
	c.cancel()
}

// Handle a request. errors from this method indicate a failure to read or
// write on the network stream, and trigger a disconnection of the connection.
func (c *conn) handle(ctx context.Context, w *response) error {
	handler, rpcErr := c.Server.handlerFor(w.req.Prog, w.req.Vers, w.req.Proc)
	if handler == nil {
		Log.Debugf("No handler for %d.%d.%d", w.req.Prog, w.req.Vers, w.req.Proc)
		if err := w.drain(ctx); err != nil {
			return err
		}
		return c.err(ctx, w, rpcErr)
	}
	appError := handler(ctx, w, c.Server.Handler)
	if drainErr := w.drain(ctx); drainErr != nil {
		return drainErr
	}
	if appError != nil && !w.responded {
		if err := c.err(ctx, w, appError); err != nil {
			return err
		}
	}
	if !w.responded {
		Log.Errorf("Handler did not indicate response status via writing or erroring")
		if err := c.err(ctx, w, &ResponseCodeSystemError{}); err != nil {
			return err
		}
	}
	return nil
}

func (c *conn) err(ctx context.Context, w *response, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	if w.err == nil {
		w.err = err
	}

	if w.responded {
		return nil
	}

	rpcErr := w.errorFmt(err)
	if writeErr := w.writeHeader(rpcErr.Code()); writeErr != nil {
		return writeErr
	}

	body, _ := rpcErr.MarshalBinary()
	return w.Write(body)
}

type request struct {
	xid uint32
	rpc.Header
	Body io.Reader
}

func (r *request) String() string {
	switch r.Prog {
	case nfsServiceID:
		if r.Vers == nfs4Version {
			switch r.Proc {
			case nfs4ProcNull:
				return fmt.Sprintf("RPC #%d (nfs4.Null)", r.xid)
			case nfs4ProcCompound:
				return fmt.Sprintf("RPC #%d (nfs4.Compound)", r.xid)
			default:
				return fmt.Sprintf("RPC #%d (nfs4.%d)", r.xid, r.Proc)
			}
		}
		return fmt.Sprintf("RPC #%d (nfs.%s)", r.xid, NFSProcedure(r.Proc))
	case mountServiceID:
		return fmt.Sprintf("RPC #%d (mount.%s)", r.xid, MountProcedure(r.Proc))
	}
	return fmt.Sprintf("RPC #%d (%d.%d)", r.xid, r.Prog, r.Proc)
}

type response struct {
	*conn
	writer    *bytes.Buffer
	responded bool
	err       error
	errorFmt  func(error) RPCError
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
		err = xdr.Write(w.writer, &rpc.AuthNull)
		if err != nil {
			return err
		}
	}

	return xdr.Write(w.writer, &code)
}

// Write a response to an xdr message
func (w *response) Write(dat []byte) error {
	if !w.responded {
		if err := w.writeHeader(ResponseCodeSuccess); err != nil {
			return err
		}
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

// bufferBody reads the rest of the request frame into memory, so the
// connection can go on to read the next request while this one is handled.
// The handler sees the same io.LimitedReader as before.
func (w *response) bufferBody() error {
	lr, ok := w.req.Body.(*io.LimitedReader)
	if !ok {
		return io.ErrUnexpectedEOF
	}
	var buf []byte
	if lr.N > 0 {
		buf = make([]byte, lr.N)
	}
	if _, err := io.ReadFull(lr, buf); err != nil {
		return err
	}
	w.req.Body = &io.LimitedReader{R: bytes.NewReader(buf), N: int64(len(buf))}
	return nil
}

// drain reads the rest of the request frame if not consumed by the handler.
func (w *response) drain(ctx context.Context) error {
	if reader, ok := w.req.Body.(*io.LimitedReader); ok {
		if reader.N == 0 {
			return nil
		}
		// Ending the connection's context closes the connection, which
		// unblocks this read.
		_, err := io.CopyN(io.Discard, w.req.Body, reader.N)
		if err == nil || err == io.EOF {
			return nil
		}
		return err
	}
	return io.ErrUnexpectedEOF
}

func (w *response) finish(ctx context.Context) error {
	select {
	case w.conn.writeSerializer <- w.writer.Bytes():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *conn) readRequestHeader(ctx context.Context, reader *bufio.Reader) (w *response, err error) {
	fragment, err := xdr.ReadUint32(reader)
	if err != nil {
		if xdrErr, ok := err.(*xdr2.UnmarshalError); ok {
			if xdrErr.Err == io.EOF {
				return nil, io.EOF
			}
		}
		return nil, err
	}
	if fragment&(1<<31) == 0 {
		Log.Warnf("Warning: haven't implemented fragment reconstruction.\n")
		return nil, ErrInputInvalid
	}
	reqLen := fragment - uint32(1<<31)
	if reqLen < 40 || reqLen > maxRequestFragmentBytes {
		return nil, ErrInputInvalid
	}

	r := io.LimitedReader{R: reader, N: int64(reqLen)}

	xid, err := xdr.ReadUint32(&r)
	if err != nil {
		return nil, err
	}
	reqType, err := xdr.ReadUint32(&r)
	if err != nil {
		return nil, err
	}
	if reqType != 0 { // 0 = request, 1 = response
		return nil, ErrInputInvalid
	}

	req := request{
		xid,
		rpc.Header{},
		&r,
	}
	// Limit authentication lengths before the decoder allocates their bodies.
	// The record's LimitedReader bounds reads, but not XDR allocations.
	if _, err = xdr2.UnmarshalLimited(&r, &req.Header, maxAuthBytes); err != nil {
		return nil, err
	}

	w = &response{
		conn:     c,
		req:      &req,
		errorFmt: basicErrorFormatter,
		// TODO: use a pool for these.
		writer: bytes.NewBuffer([]byte{}),
	}
	return w, nil
}
