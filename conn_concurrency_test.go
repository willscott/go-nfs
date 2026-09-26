package nfs

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// A private RPC program with one procedure that blocks until released and
// one that answers at once, to observe how a connection schedules requests.
const (
	testProgram  = 0x20000123
	testProcSlow = 1
	testProcFast = 2
)

var releaseSlow = make(chan chan struct{}, 1)

func init() {
	_ = RegisterMessageHandler(testProgram, testProcSlow, func(ctx context.Context, w *response, _ Handler) error {
		release := <-releaseSlow
		select {
		case <-release:
		case <-ctx.Done():
		}
		return w.Write([]byte{})
	})
	_ = RegisterMessageHandler(testProgram, testProcFast, func(ctx context.Context, w *response, _ Handler) error {
		return w.Write([]byte{})
	})
}

// startTestServer serves s on a local port and returns a connection to it.
func startTestServer(t *testing.T, s *Server) net.Conn {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = s.Serve(l) }()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// sendCall writes an RPC call with AUTH_NONE credentials and no arguments.
func sendCall(t *testing.T, c net.Conn, xid, proc uint32) {
	t.Helper()
	fields := []uint32{
		xid, 0, 2, // xid, CALL, RPC version 2
		testProgram, 3, proc, // RegisterMessageHandler serves every version
		0, 0, // credential: AUTH_NONE, empty
		0, 0, // verifier: AUTH_NONE, empty
	}
	msg := make([]byte, 4+4*len(fields))
	binary.BigEndian.PutUint32(msg, uint32(4*len(fields))|1<<31)
	for i, f := range fields {
		binary.BigEndian.PutUint32(msg[4+4*i:], f)
	}
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
}

// readReplyXID reads one reply record and returns its XID, or false if none
// arrives within the timeout.
func readReplyXID(t *testing.T, c net.Conn, timeout time.Duration) (uint32, bool) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	var mark [4]byte
	if _, err := io.ReadFull(c, mark[:]); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return 0, false
		}
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(mark[:])&^(1<<31))
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint32(body), true
}

func TestConcurrentHandlersAnswerFastRequestsFirst(t *testing.T) {
	release := make(chan struct{})
	releaseSlow <- release
	c := startTestServer(t, &Server{ConcurrentHandlers: 4})

	sendCall(t, c, 1, testProcSlow)
	sendCall(t, c, 2, testProcFast)

	// The fast request is answered while the slow one is still running.
	if xid, ok := readReplyXID(t, c, 5*time.Second); !ok || xid != 2 {
		t.Fatalf("first reply: xid %d (received %v), want 2 while request 1 is blocked", xid, ok)
	}
	close(release)
	if xid, ok := readReplyXID(t, c, 5*time.Second); !ok || xid != 1 {
		t.Fatalf("second reply: xid %d (received %v), want 1", xid, ok)
	}
}

func TestDefaultHandlesRequestsInOrder(t *testing.T) {
	release := make(chan struct{})
	releaseSlow <- release
	c := startTestServer(t, &Server{})

	sendCall(t, c, 1, testProcSlow)
	sendCall(t, c, 2, testProcFast)

	// Without ConcurrentHandlers, the fast request waits behind the slow one.
	if xid, ok := readReplyXID(t, c, 300*time.Millisecond); ok {
		t.Fatalf("got reply %d while request 1 was blocked; want requests handled in order", xid)
	}
	close(release)
	for _, want := range []uint32{1, 2} {
		if xid, ok := readReplyXID(t, c, 5*time.Second); !ok || xid != want {
			t.Fatalf("reply: xid %d (received %v), want %d", xid, ok, want)
		}
	}
}

// A record-marking length far beyond any real request is refused before
// anything is buffered for it.
func TestConcurrentHandlersRejectOversizedRequest(t *testing.T) {
	c := startTestServer(t, &Server{ConcurrentHandlers: 4})
	var mark [4]byte
	binary.BigEndian.PutUint32(mark[:], (maxRequestFragmentBytes+1)|1<<31)
	if _, err := c.Write(mark[:]); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection stayed open after an oversized request")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("server neither answered nor closed the connection")
	}
}
