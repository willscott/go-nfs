package nfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"time"
)

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

// Serve listens on the provided listener port for incoming client requests.
func (s *Server) Serve(l net.Listener) error {
	defer l.Close()
	baseCtx := context.Background()
	if s.Context != nil {
		baseCtx = s.Context
	}
	if bytes.Equal(s.ID[:], []byte{0, 0, 0, 0, 0, 0, 0, 0}) {
		if _, err := rand.Reader.Read(s.ID[:]); err != nil {
			return err
		}
	}

	var tempDelay time.Duration

	for {
		conn, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		c := s.newConn(conn)
		go c.serve(baseCtx)
	}
}

func (s *Server) newConn(nc net.Conn) *conn {
	c := &conn{
		Server: s,
		Conn:   nc,
	}
	return c
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
