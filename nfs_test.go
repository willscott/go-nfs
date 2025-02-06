package nfs_test

import (
	"bytes"
	"fmt"
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

func TestNFS(t *testing.T) {
	if testing.Verbose() {
		util.DefaultLogger.SetDebug(true)
	}

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
	if _, err = mf.Read(buf[:]); err != nil {
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
