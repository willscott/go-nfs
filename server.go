package nfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrServerClosed is returned by Serve after a call to Shutdown or Close.
var ErrServerClosed = errors.New("nfs: Server closed")

// Server is a handle to the listening NFS server.
type Server struct {
	Handler
	ID [8]byte
	// ConcurrentHandlers is how many requests on one connection may be
	// handled at the same time. Zero or one handles them one at a time, in
	// order, as before. A larger value requires the Handler and the
	// filesystems it returns to be safe for concurrent use.
	ConcurrentHandlers int
	// EnabledNFSVersions limits the NFS program versions served (3, 4).
	// Empty serves every registered version.
	EnabledNFSVersions []uint32
	context.Context

	// NFSv4Locker holds the byte ranges behind NFSv4 LOCK, LOCKT and LOCKU.
	// Nil uses an in-memory table private to this server (NewMemoryLocker).
	// Supply a table shared with other protocol servers so locks conflict
	// across protocols. NFSv4 clients' locks are released when their leases
	// expire; a table that outlives the Server keeps those it held when it
	// stopped, since the clients holding them are gone with it.
	NFSv4Locker ByteRangeLocker

	// NFSv4 protocol state (open and lock stateids, client leases),
	// created on first use.
	nfs4StateMgr  *nfs4StateManager
	nfs4StateOnce sync.Once

	// inShutdown is set by Shutdown and Close; the server then accepts no
	// new connections and its connections take no new requests.
	inShutdown atomic.Bool
	mu         sync.Mutex
	listeners  map[*net.Listener]struct{}
	conns      map[*conn]struct{}
}

// RegisterMessageHandler registers a handler for a procedure of an RPC
// program, whatever version a call names. A handler registered for the
// call's own version with RegisterVersionedMessageHandler takes precedence.
func RegisterMessageHandler(protocol uint32, proc uint32, handler HandleFunc) error {
	return registerHandler(registeredHandlerID{protocol, anyVersion, proc}, handler)
}

// RegisterVersionedMessageHandler registers a handler for a specific RPC
// program, version, and procedure.
func RegisterVersionedMessageHandler(protocol uint32, version uint32, proc uint32, handler HandleFunc) error {
	if version == anyVersion {
		return errors.New("invalid version")
	}
	return registerHandler(registeredHandlerID{protocol, version, proc}, handler)
}

func registerHandler(id registeredHandlerID, handler HandleFunc) error {
	if registeredHandlers == nil {
		registeredHandlers = make(map[registeredHandlerID]HandleFunc)
	}
	for k := range registeredHandlers {
		if k.protocol == id.protocol && k.proc == id.proc &&
			(k.version == id.version || k.version == anyVersion || id.version == anyVersion) {
			return errors.New("already registered")
		}
	}
	registeredHandlers[id] = handler
	return nil
}

// HandleFunc represents a handler for a specific protocol message.
type HandleFunc func(ctx context.Context, w *response, userHandler Handler) error

// TODO: store directly as a uint64 for more efficient lookups
type registeredHandlerID struct {
	protocol uint32
	version  uint32
	proc     uint32
}

// anyVersion marks a handler registered by RegisterMessageHandler, which
// serves every version of its program.
const anyVersion = ^uint32(0)

var registeredHandlers map[registeredHandlerID]HandleFunc

// Serve accepts connections on l and serves each one on its own goroutine.
//
// Serve returns only once every connection it accepted has finished. After
// Shutdown or Close it returns ErrServerClosed. If l fails some other way,
// Serve closes the connections it accepted and returns the error.
//
// The Server's Context is the parent of the context each request is
// handled with.
func (s *Server) Serve(l net.Listener) error {
	l = &onceCloseListener{Listener: l}
	defer func() { _ = l.Close() }()
	if !s.trackListener(&l, true) {
		return ErrServerClosed
	}
	defer s.trackListener(&l, false)

	baseCtx := context.Background()
	if s.Context != nil {
		baseCtx = s.Context
	}
	if bytes.Equal(s.ID[:], []byte{0, 0, 0, 0, 0, 0, 0, 0}) {
		if _, err := rand.Reader.Read(s.ID[:]); err != nil {
			return err
		}
	}

	serveCtx, closeConns := context.WithCancel(baseCtx)
	defer closeConns()
	var conns sync.WaitGroup

	var tempDelay time.Duration
	for {
		nc, err := l.Accept()
		if err != nil {
			if s.inShutdown.Load() {
				// Shutdown or Close is ending the connections; wait for them.
				conns.Wait()
				return ErrServerClosed
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				tempDelay = min(tempDelay, 1*time.Second)
				time.Sleep(tempDelay)
				continue
			}
			closeConns()
			conns.Wait()
			return err
		}
		tempDelay = 0

		connCtx, cancel := context.WithCancel(serveCtx)
		c := s.newConn(nc, cancel)
		if !s.trackConn(c, true) {
			// Shutting down: the listener is closed, so Accept fails next.
			cancel()
			_ = nc.Close()
			continue
		}
		conns.Go(func() {
			defer s.trackConn(c, false)
			c.serve(connCtx)
		})
	}
}

// Shutdown stops the server gracefully. It closes the listeners, lets each
// connection finish and answer the requests it is already handling, and
// then closes it. Shutdown returns when every connection has closed.
//
// If ctx ends first, Shutdown closes the remaining connections as Close
// does and returns ctx's error. NFS clients resend unanswered requests, so
// a client of a server that is restarted loses nothing but time.
func (s *Server) Shutdown(ctx context.Context) error {
	s.inShutdown.Store(true)
	s.mu.Lock()
	err := s.closeListenersLocked()
	for c := range s.conns {
		c.interruptIdleRead()
	}
	s.mu.Unlock()

	const maxPoll = 500 * time.Millisecond
	poll := time.Millisecond
	timer := time.NewTimer(poll)
	defer timer.Stop()
	for {
		if s.numConns() == 0 {
			return err
		}
		select {
		case <-ctx.Done():
			_ = s.Close()
			return ctx.Err()
		case <-timer.C:
			poll = min(poll*2, maxPoll)
			timer.Reset(poll)
		}
	}
}

// Close stops the server at once. It closes the listeners and every
// connection, and cancels the context of each request still being handled.
// Close does not wait for handlers to return; Serve does. For a graceful
// stop, use Shutdown.
func (s *Server) Close() error {
	s.inShutdown.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.closeListenersLocked()
	for c := range s.conns {
		c.cancel()
	}
	return err
}

// trackListener adds or removes a listener Shutdown and Close must close.
// It reports false, adding nothing, once the server is shutting down.
func (s *Server) trackListener(l *net.Listener, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !add {
		delete(s.listeners, l)
		return true
	}
	if s.inShutdown.Load() {
		return false
	}
	if s.listeners == nil {
		s.listeners = make(map[*net.Listener]struct{})
	}
	s.listeners[l] = struct{}{}
	return true
}

// trackConn adds or removes a connection Shutdown and Close must end. It
// reports false, adding nothing, once the server is shutting down.
func (s *Server) trackConn(c *conn, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !add {
		delete(s.conns, c)
		return true
	}
	if s.inShutdown.Load() {
		return false
	}
	if s.conns == nil {
		s.conns = make(map[*conn]struct{})
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) numConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) closeListenersLocked() error {
	var err error
	for l := range s.listeners {
		if cerr := (*l).Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func (s *Server) newConn(nc net.Conn, cancel context.CancelFunc) *conn {
	return &conn{
		Server: s,
		Conn:   nc,
		cancel: cancel,
	}
}

// onceCloseListener lets both Serve and Close close a listener.
type onceCloseListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *onceCloseListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}

// handlerFor finds the handler for a call. When there is none, it returns
// the RPC error to answer with: PROG_MISMATCH, naming the versions served,
// for a version of a known program that is not served, and PROC_UNAVAIL
// otherwise.
//
// TODO: keep an immutable map for each server instance to have less
// chance of races.
func (s *Server) handlerFor(prog uint32, version uint32, proc uint32) (HandleFunc, RPCError) {
	if s.versionAllowed(prog, version) {
		if h, ok := registeredHandlers[registeredHandlerID{prog, version, proc}]; ok {
			return h, nil
		}
		if h, ok := registeredHandlers[registeredHandlerID{prog, anyVersion, proc}]; ok {
			return h, nil
		}
	}
	if low, high, ok := s.versionsServed(prog); ok && (version < low || version > high || !s.versionAllowed(prog, version)) {
		return nil, &ProgMismatchError{Low: low, High: high}
	}
	return nil, &ResponseCodeProcUnavailableError{}
}

// versionsServed is the range of versions of prog this server answers. It
// is not known for a program with a handler for any version.
func (s *Server) versionsServed(prog uint32) (low, high uint32, ok bool) {
	for k := range registeredHandlers {
		if k.protocol != prog {
			continue
		}
		if k.version == anyVersion {
			return 0, 0, false
		}
		if !s.versionAllowed(prog, k.version) {
			continue
		}
		if !ok || k.version < low {
			low = k.version
		}
		if !ok || k.version > high {
			high = k.version
		}
		ok = true
	}
	return low, high, ok
}

func (s *Server) versionAllowed(prog uint32, version uint32) bool {
	if prog != nfsServiceID && prog != mountServiceID {
		return true
	}
	return s.nfsVersionAllowed(prog, version)
}

func (s *Server) nfsVersionAllowed(prog uint32, version uint32) bool {
	if len(s.EnabledNFSVersions) == 0 {
		return true
	}
	if prog == mountServiceID {
		version = 3
	}
	for _, enabled := range s.EnabledNFSVersions {
		if enabled == version {
			return true
		}
	}
	return false
}

// Serve is a singleton listener paralleling http.Serve
func Serve(l net.Listener, handler Handler) error {
	srv := &Server{Handler: handler}
	return srv.Serve(l)
}
