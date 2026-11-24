package nfs

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// testProcUntilCanceled blocks until its request's context is canceled, then
// lingers briefly, so a Serve that returns early would see it still running.
const testProcUntilCanceled = 3

var (
	untilCanceledStarted = make(chan struct{}, 8)
	untilCanceledRunning atomic.Int32
)

func init() {
	_ = RegisterMessageHandler(testProgram, testProcUntilCanceled, func(ctx context.Context, w *response, _ Handler) error {
		untilCanceledRunning.Add(1)
		defer untilCanceledRunning.Add(-1)
		untilCanceledStarted <- struct{}{}
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		return w.Write([]byte{})
	})
}

// serveForTest serves s on a local port. It returns a connection to it and
// a channel that receives Serve's result.
func serveForTest(t *testing.T, s *Server) (*net.TCPConn, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		served <- s.Serve(l)
	}()
	t.Cleanup(func() { _ = s.Close(); <-done })
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.(*net.TCPConn), served
}

func waitServe(t *testing.T, served <-chan error) error {
	t.Helper()
	select {
	case err := <-served:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

// expectClosed checks that the server closes c without sending more.
func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var b [1]byte
	if n, err := c.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("read %d bytes, %v; want the server to close the connection", n, err)
	}
}

func TestCloseWaitsForConnections(t *testing.T) {
	for _, limit := range []int{0, 4} {
		t.Run("", func(t *testing.T) {
			s := &Server{ConcurrentHandlers: limit}
			c, served := serveForTest(t, s)
			sendCall(t, c, 1, testProcUntilCanceled)
			<-untilCanceledStarted

			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := waitServe(t, served); !errors.Is(err, ErrServerClosed) {
				t.Fatalf("Serve: %v, want ErrServerClosed", err)
			}
			if n := untilCanceledRunning.Load(); n != 0 {
				t.Fatalf("Serve returned with %d handlers running", n)
			}
		})
	}
}

func TestShutdownAnswersRequestsInFlight(t *testing.T) {
	for _, limit := range []int{0, 4} {
		t.Run("", func(t *testing.T) {
			s := &Server{ConcurrentHandlers: limit}
			c, served := serveForTest(t, s)
			release := make(chan struct{})
			releaseSlow <- release
			sendCall(t, c, 7, testProcSlow)
			// Let the server read the call before shutting down.
			time.Sleep(50 * time.Millisecond)

			shutdown := make(chan error, 1)
			go func() { shutdown <- s.Shutdown(context.Background()) }()
			select {
			case err := <-shutdown:
				t.Fatalf("Shutdown returned %v with a request in flight", err)
			case <-time.After(50 * time.Millisecond):
			}

			close(release)
			if xid, ok := readReplyXID(t, c, 5*time.Second); !ok || xid != 7 {
				t.Fatalf("reply %d, %v; want the in-flight request answered", xid, ok)
			}
			expectClosed(t, c)
			if err := <-shutdown; err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if err := waitServe(t, served); !errors.Is(err, ErrServerClosed) {
				t.Fatalf("Serve: %v, want ErrServerClosed", err)
			}
		})
	}
}

func TestShutdownClosesIdleConnections(t *testing.T) {
	s := &Server{}
	c, served := serveForTest(t, s)
	sendCall(t, c, 1, testProcFast)
	if _, ok := readReplyXID(t, c, 5*time.Second); !ok {
		t.Fatal("no reply")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, c)
	if err := waitServe(t, served); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v, want ErrServerClosed", err)
	}
}

func TestShutdownDeadlineClosesConnections(t *testing.T) {
	s := &Server{}
	c, served := serveForTest(t, s)
	sendCall(t, c, 1, testProcUntilCanceled)
	<-untilCanceledStarted

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown: %v, want DeadlineExceeded", err)
	}
	if err := waitServe(t, served); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v, want ErrServerClosed", err)
	}
	if n := untilCanceledRunning.Load(); n != 0 {
		t.Fatalf("Serve returned with %d handlers running", n)
	}
}

func TestServeAfterShutdown(t *testing.T) {
	s := &Server{}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(l); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v, want ErrServerClosed", err)
	}
	if _, err := l.Accept(); err == nil {
		t.Fatal("Serve left the listener open")
	}
}

// A client that sends its last request and closes its side of the
// connection still gets the reply.
func TestReplyAfterClientCloseWrite(t *testing.T) {
	for _, limit := range []int{0, 4} {
		t.Run("", func(t *testing.T) {
			c, _ := serveForTest(t, &Server{ConcurrentHandlers: limit})
			sendCall(t, c, 9, testProcFast)
			if err := c.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			if xid, ok := readReplyXID(t, c, 5*time.Second); !ok || xid != 9 {
				t.Fatalf("reply %d, %v; want 9", xid, ok)
			}
			expectClosed(t, c)
		})
	}
}
