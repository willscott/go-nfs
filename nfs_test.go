package nfs_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"
	"github.com/willscott/go-nfs/helpers/memfs"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/util"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type OpenArgs struct {
	File string
	Flag int
	Perm os.FileMode
}

func (o *OpenArgs) String() string {
	return fmt.Sprintf("\"%s\"; %05xd %s", o.File, o.Flag, o.Perm)
}

// NewTrackingFS wraps fs to detect file handle leaks.
func NewTrackingFS(fs billy.Filesystem) *trackingFS {
	return &trackingFS{Filesystem: fs, open: make(map[int64]OpenArgs)}
}

// trackingFS wraps a Filesystem to detect file handle leaks.
type trackingFS struct {
	billy.Filesystem
	mu   sync.Mutex
	open map[int64]OpenArgs
}

func (t *trackingFS) ListOpened() []OpenArgs {
	t.mu.Lock()
	defer t.mu.Unlock()
	ret := make([]OpenArgs, 0, len(t.open))
	for _, o := range t.open {
		ret = append(ret, o)
	}
	return ret
}

func (t *trackingFS) Create(filename string) (billy.File, error) {
	return t.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (t *trackingFS) Open(filename string) (billy.File, error) {
	return t.OpenFile(filename, os.O_RDONLY, 0)
}

func (t *trackingFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	open, err := t.Filesystem.OpenFile(filename, flag, perm)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	id := rand.Int63()
	t.open[id] = OpenArgs{filename, flag, perm}
	closer := func() {
		delete(t.open, id)
	}
	open = &trackingFile{
		File:    open,
		onClose: closer,
	}
	return open, err
}

type trackingFile struct {
	billy.File
	onClose func()
}

func (f *trackingFile) Close() error {
	f.onClose()
	return f.File.Close()
}

var setDebugOnce sync.Once

func TestNFS(t *testing.T) {
	setDebugOnce.Do(func() {
		if testing.Verbose() {
			util.DefaultLogger.SetDebug(true)
		}
	})

	// make an empty in-memory server.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	mem := NewTrackingFS(memfs.New())

	defer func() {
		if opened := mem.ListOpened(); len(opened) > 0 {
			t.Errorf("Unclosed files: %v", opened)
		}
	}()

	// File needs to exist in the root for memfs to acknowledge the root exists.
	r, _ := mem.Create("/test")
	r.Close()

	handler := helpers.NewNullAuthHandler(mem)
	cacheHelper := helpers.NewCachingHandler(handler, 1024)
	go func() {
		_ = nfs.Serve(listener, cacheHelper)
	}()

	c, err := rpc.DialTCP(listener.Addr().Network(), listener.Addr().(*net.TCPAddr).String(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = mounter.Unmount()
	}()

	_, err = target.FSInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Validate sample file creation
	_, err = target.Create("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := mem.Stat("/helloworld.txt"); err != nil {
		t.Fatal(err)
	} else {
		if info.Size() != 0 || info.Mode().Perm() != 0666 {
			t.Fatal("incorrect creation.")
		}
	}

	// Validate writing to a file.
	f, err := target.OpenFile("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := []byte("hello world")
	_, err = f.Write(b)
	if err != nil {
		t.Fatal(err)
	}

	mf, err := target.Open("/helloworld.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	buf := make([]byte, len(b))
	if _, err = mf.Read(buf[:]); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, b) {
		t.Fatal("written does not match expected")
	}

	// for test nfs.ReadDirPlus in case of many files
	dirF1, err := mem.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	shouldBeNames := []string{}
	for _, f := range dirF1 {
		shouldBeNames = append(shouldBeNames, f.Name())
	}
	for i := 0; i < 2000; i++ {
		fName := fmt.Sprintf("f-%04d.txt", i)
		shouldBeNames = append(shouldBeNames, fName)
		f, err := mem.Create(fName)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	manyEntitiesPlus, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatal(err)
	}
	actualBeNamesPlus := []string{}
	for _, e := range manyEntitiesPlus {
		actualBeNamesPlus = append(actualBeNamesPlus, e.Name())
	}

	as := sort.StringSlice(shouldBeNames)
	bs := sort.StringSlice(actualBeNamesPlus)
	as.Sort()
	bs.Sort()
	if !reflect.DeepEqual(as, bs) {
		t.Fatal("nfs.ReadDirPlus error")
	}

	// for test nfs.ReadDir in case of many files
	manyEntities, err := readDir(target, "/")
	if err != nil {
		t.Fatal(err)
	}
	actualBeNames := []string{}
	for _, e := range manyEntities {
		actualBeNames = append(actualBeNames, e.FileName)
	}

	as2 := sort.StringSlice(shouldBeNames)
	bs2 := sort.StringSlice(actualBeNames)
	as2.Sort()
	bs2.Sort()
	if !reflect.DeepEqual(as2, bs2) {
		fmt.Printf("should be %v\n", as2)
		fmt.Printf("actual be %v\n", bs2)
		t.Fatal("nfs.ReadDir error")
	}

	// confirm rename works as expected
	oldFA, _, err := target.Lookup("/f-0010.txt", false)
	if err != nil {
		t.Fatal(err)
	}

	if err := target.Rename("/f-0010.txt", "/g-0010.txt"); err != nil {
		t.Fatal(err)
	}
	new, _, err := target.Lookup("/g-0010.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if new.Sys() != oldFA.Sys() {
		t.Fatal("rename failed to update")
	}
	_, _, err = target.Lookup("/f-0010.txt", false)
	if err == nil {
		t.Fatal("old handle should be invalid")
	}

	// for test nfs.ReadDirPlus in case of empty directory
	_, err = target.Mkdir("/empty", 0755)
	if err != nil {
		t.Fatal(err)
	}

	emptyEntitiesPlus, err := target.ReadDirPlus("/empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEntitiesPlus) != 0 {
		t.Fatal("nfs.ReadDirPlus error reading empty dir")
	}

	// for test nfs.ReadDir in case of empty directory
	emptyEntities, err := readDir(target, "/empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEntities) != 0 {
		t.Fatal("nfs.ReadDir error reading empty dir")
	}
}

type readDirEntry struct {
	FileId   uint64
	FileName string
	Cookie   uint64
}

// readDir implementation "appropriated" from go-nfs-client implementation of READDIRPLUS
func readDir(target *nfsc.Target, dir string) ([]*readDirEntry, error) {
	_, fh, err := target.Lookup(dir)
	if err != nil {
		return nil, err
	}

	type readDirArgs struct {
		rpc.Header
		Handle      []byte
		Cookie      uint64
		CookieVerif uint64
		Count       uint32
	}

	type readDirList struct {
		IsSet bool         `xdr:"union"`
		Entry readDirEntry `xdr:"unioncase=1"`
	}

	type readDirListOK struct {
		DirAttrs   nfsc.PostOpAttr
		CookieVerf uint64
	}

	cookie := uint64(0)
	cookieVerf := uint64(0)
	eof := false

	var entries []*readDirEntry
	for !eof {
		res, err := target.Call(&readDirArgs{
			Header: rpc.Header{
				Rpcvers: 2,
				Vers:    nfsc.Nfs3Vers,
				Prog:    nfsc.Nfs3Prog,
				Proc:    uint32(nfs.NFSProcedureReadDir),
				Cred:    rpc.AuthNull,
				Verf:    rpc.AuthNull,
			},
			Handle:      fh,
			Cookie:      cookie,
			CookieVerif: cookieVerf,
			Count:       4096,
		})
		if err != nil {
			return nil, err
		}

		status, err := xdr.ReadUint32(res)
		if err != nil {
			return nil, err
		}

		if err = nfsc.NFS3Error(status); err != nil {
			return nil, err
		}

		dirListOK := new(readDirListOK)
		if err = xdr.Read(res, dirListOK); err != nil {
			return nil, err
		}

		for {
			var item readDirList
			if err = xdr.Read(res, &item); err != nil {
				return nil, err
			}

			if !item.IsSet {
				break
			}

			cookie = item.Entry.Cookie
			if item.Entry.FileName == "." || item.Entry.FileName == ".." {
				continue
			}
			entries = append(entries, &item.Entry)
		}

		if err = xdr.Read(res, &eof); err != nil {
			return nil, err
		}

		cookieVerf = dirListOK.CookieVerf
	}

	return entries, nil
}

// nfsRead issues a raw NFS READ RPC and returns the count, eof flag, and data.
func nfsRead(target *nfsc.Target, filePath string, offset uint64, count uint32) (uint32, bool, []byte, error) {
	_, fh, err := target.Lookup(filePath)
	if err != nil {
		return 0, false, nil, err
	}

	type readArgs struct {
		rpc.Header
		Handle []byte
		Offset uint64
		Count  uint32
	}

	res, err := target.Call(&readArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Vers:    nfsc.Nfs3Vers,
			Prog:    nfsc.Nfs3Prog,
			Proc:    uint32(nfs.NFSProcedureRead),
			Cred:    rpc.AuthNull,
			Verf:    rpc.AuthNull,
		},
		Handle: fh,
		Offset: offset,
		Count:  count,
	})
	if err != nil {
		return 0, false, nil, err
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return 0, false, nil, err
	}
	if err = nfsc.NFS3Error(status); err != nil {
		return 0, false, nil, err
	}

	// Read response using same structure as the NFS client
	type readRes struct {
		Attr  nfsc.PostOpAttr
		Count uint32
		EOF   uint32
		Data  struct {
			Length uint32
		}
	}
	var readResp readRes
	if err = xdr.Read(res, &readResp); err != nil {
		return 0, false, nil, err
	}
	data := make([]byte, readResp.Data.Length)
	if readResp.Data.Length > 0 {
		if _, err = io.ReadFull(res, data); err != nil {
			return 0, false, nil, err
		}
	}

	return readResp.Count, readResp.EOF != 0, data, nil
}

func TestReadEOF(t *testing.T) {
	setDebugOnce.Do(func() {
		if testing.Verbose() {
			util.DefaultLogger.SetDebug(true)
		}
	})

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	mem := memfs.New()

	// Create a 64KB file with random content.
	const fileSize = 64 * 1024
	fileData := make([]byte, fileSize)
	if _, err := rand.Read(fileData); err != nil {
		t.Fatal(err)
	}
	f, err := mem.Create("/testfile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(fileData); err != nil {
		t.Fatal(err)
	}
	f.Close()

	handler := helpers.NewNullAuthHandler(mem)
	cacheHelper := helpers.NewCachingHandler(handler, 1024)
	go func() {
		_ = nfs.Serve(listener, cacheHelper)
	}()

	c, err := rpc.DialTCP(listener.Addr().Network(), listener.Addr().(*net.TCPAddr).String(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = mounter.Unmount()
	}()

	// Small mid-file read: should NOT set EOF
	cnt, eof, data, err := nfsRead(target, "/testfile", 0, 16*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 16*1024 {
		t.Fatalf("small mid-file read: expected count %d, got %d", 16*1024, cnt)
	}
	if eof {
		t.Fatal("small mid-file read: EOF should not be set")
	}
	if !bytes.Equal(data, fileData[:16*1024]) {
		t.Fatal("small mid-file read: data mismatch")
	}

	// Small read that reaches exactly EOF: should set EOF
	cnt, eof, data, err = nfsRead(target, "/testfile", 48*1024, 16*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 16*1024 {
		t.Fatalf("small end-of-file read: expected count %d, got %d", 16*1024, cnt)
	}
	if !eof {
		t.Fatal("small end-of-file read: EOF should be set")
	}
	if !bytes.Equal(data, fileData[48*1024:]) {
		t.Fatal("small end-of-file read: data mismatch")
	}

	// Large mid-file read: should NOT set EOF
	cnt, eof, data, err = nfsRead(target, "/testfile", 0, 40*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 40*1024 {
		t.Fatalf("mid-file read: expected count %d, got %d", 40*1024, cnt)
	}
	if eof {
		t.Fatal("mid-file read: EOF should not be set")
	}
	if !bytes.Equal(data, fileData[:40*1024]) {
		t.Fatal("mid-file read: data mismatch")
	}

	// Read that reaches exactly the end of file (offset+count == filesize): should set EOF
	cnt, eof, data, err = nfsRead(target, "/testfile", 24*1024, 40*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 40*1024 {
		t.Fatalf("end-of-file read: expected count %d, got %d", 40*1024, cnt)
	}
	if !eof {
		t.Fatal("end-of-file read: EOF should be set")
	}
	if !bytes.Equal(data, fileData[24*1024:]) {
		t.Fatal("end-of-file read: data mismatch")
	}

	// Read that extends past EOF (count > remaining): should set EOF with trimmed count
	cnt, eof, data, err = nfsRead(target, "/testfile", 60*1024, 40*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 4*1024 {
		t.Fatalf("past-EOF read: expected count %d, got %d", 4*1024, cnt)
	}
	if !eof {
		t.Fatal("past-EOF read: EOF should be set")
	}
	if !bytes.Equal(data, fileData[60*1024:]) {
		t.Fatal("past-EOF read: data mismatch")
	}

	// Read at offset == filesize: should set EOF with count=0
	cnt, eof, _, err = nfsRead(target, "/testfile", fileSize, 40*1024)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("at-EOF read: expected count 0, got %d", cnt)
	}
	if !eof {
		t.Fatal("at-EOF read: EOF should be set")
	}
}
