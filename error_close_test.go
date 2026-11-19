package nfs

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type closeTestFile struct {
	billy.File

	seekErr     error
	writeErr    error
	writeN      int
	truncateErr error
	closeErr    error

	closeCalls    int
	truncateCalls int
	contents      bytes.Buffer
	truncateSize  int64
}

func (f *closeTestFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekErr != nil {
		return 0, f.seekErr
	}
	return offset, nil
}

func (f *closeTestFile) Write(data []byte) (int, error) {
	n := len(data)
	if f.writeErr != nil && f.writeN < n {
		n = f.writeN
	}
	_, _ = f.contents.Write(data[:n])
	return n, f.writeErr
}

func (f *closeTestFile) Truncate(size int64) error {
	f.truncateCalls++
	f.truncateSize = size
	return f.truncateErr
}

func (f *closeTestFile) Close() error {
	f.closeCalls++
	return f.closeErr
}

type closeTestInfo struct {
	mode os.FileMode
	size int64
}

func (i *closeTestInfo) Name() string       { return "alpha.txt" }
func (i *closeTestInfo) Size() int64        { return i.size }
func (i *closeTestInfo) Mode() os.FileMode  { return i.mode }
func (i *closeTestInfo) ModTime() time.Time { return time.Time{} }
func (i *closeTestInfo) IsDir() bool        { return i.mode.IsDir() }
func (i *closeTestInfo) Sys() any           { return nil }

type closeTestFS struct {
	billy.Filesystem

	file *closeTestFile
	info closeTestInfo
}

func (f *closeTestFS) Capabilities() billy.Capability { return billy.WriteCapability }
func (f *closeTestFS) Join(elem ...string) string     { return path.Join(elem...) }
func (f *closeTestFS) Stat(string) (os.FileInfo, error) {
	return &f.info, nil
}
func (f *closeTestFS) Lstat(string) (os.FileInfo, error) {
	return &f.info, nil
}
func (f *closeTestFS) OpenFile(string, int, os.FileMode) (billy.File, error) {
	return f.file, nil
}

type closeTestHandler struct {
	Handler
	fs billy.Filesystem
}

func (h closeTestHandler) FromHandle([]byte) (billy.Filesystem, []string, error) {
	return h.fs, []string{"alpha.txt"}, nil
}

func callOnWriteForCloseTest(t *testing.T, file *closeTestFile, offset uint64, data []byte) (*response, error) {
	t.Helper()
	body := bytes.NewBuffer(nil)
	args := writeArgs{Offset: offset, Count: uint32(len(data)), How: uint32(fileSync), Data: data}
	if err := xdr.Write(body, &args); err != nil {
		t.Fatalf("encode write request: %v", err)
	}
	w := &response{
		conn:   &conn{Server: &Server{}},
		writer: bytes.NewBuffer(nil),
		req:    &request{Body: body},
	}
	fs := &closeTestFS{file: file, info: closeTestInfo{mode: 0o600, size: 16}}
	return w, onWrite(context.Background(), w, closeTestHandler{fs: fs})
}

func requireDirectNFSStatus(t *testing.T, err error, want NFSStatus) {
	t.Helper()
	statusErr, ok := err.(*NFSStatusError)
	if !ok {
		t.Fatalf("error type = %T, want top-level *NFSStatusError (error = %v)", err, err)
	}
	if statusErr.NFSStatus != want {
		t.Fatalf("NFS status = %v, want %v (error = %v)", statusErr.NFSStatus, want, err)
	}
}

func TestOnWriteClosesFileExactlyOnce(t *testing.T) {
	seekErr := errors.New("injected seek failure")
	writeErr := errors.New("injected write failure")
	closeErr := errors.New("injected close failure")

	tests := []struct {
		name         string
		offset       uint64
		data         []byte
		file         *closeTestFile
		wantStatus   NFSStatus
		wantErrors   []error
		wantContents string
	}{
		{
			name: "seek failure", offset: 1, data: []byte("Z"),
			file: &closeTestFile{seekErr: seekErr}, wantStatus: NFSStatusIO, wantErrors: []error{seekErr},
		},
		{
			name: "seek and close failure", offset: 1, data: []byte("Z"),
			file: &closeTestFile{seekErr: seekErr, closeErr: closeErr}, wantStatus: NFSStatusIO, wantErrors: []error{seekErr, closeErr},
		},
		{
			name: "write failure", data: []byte("XY"),
			file: &closeTestFile{writeErr: writeErr}, wantStatus: NFSStatusIO, wantErrors: []error{writeErr},
		},
		{
			name: "partial write and close failure", data: []byte("XY"),
			file: &closeTestFile{writeErr: writeErr, writeN: 1, closeErr: closeErr}, wantStatus: NFSStatusIO,
			wantErrors: []error{writeErr, closeErr}, wantContents: "X",
		},
		{
			name: "success", data: []byte("XY"), file: &closeTestFile{}, wantContents: "XY",
		},
		{
			name: "close failure after write", data: []byte("XY"),
			file: &closeTestFile{closeErr: closeErr}, wantStatus: NFSStatusIO, wantErrors: []error{closeErr}, wantContents: "XY",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w, err := callOnWriteForCloseTest(t, test.file, test.offset, test.data)
			if test.file.closeCalls != 1 {
				t.Errorf("Close calls = %d, want exactly 1", test.file.closeCalls)
			}
			if got := test.file.contents.String(); got != test.wantContents {
				t.Errorf("written contents = %q, want %q", got, test.wantContents)
			}
			if len(test.wantErrors) == 0 {
				if err != nil {
					t.Fatalf("onWrite error = %v, want success", err)
				}
				if !w.responded {
					t.Fatal("successful onWrite did not write a response")
				}
				return
			}
			requireDirectNFSStatus(t, err, test.wantStatus)
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Errorf("onWrite error = %v, want errors.Is(%v)", err, wantErr)
				}
			}
			if w.responded {
				t.Fatal("failed onWrite wrote a success response")
			}
		})
	}
}

func TestSetFileAttributesApplyClosesFileExactlyOnce(t *testing.T) {
	truncateErr := errors.New("injected truncate failure")
	closeErr := errors.New("injected close failure")
	huge := uint64(math.MaxInt64) + 1
	valid := uint64(4)

	tests := []struct {
		name          string
		size          uint64
		file          *closeTestFile
		wantStatus    *NFSStatus
		wantErrors    []error
		wantTruncates int
	}{
		{
			name: "out of range size", size: huge, file: &closeTestFile{},
			wantStatus: nfsStatusPointer(NFSStatusInval), wantErrors: []error{os.ErrInvalid},
		},
		{
			name: "out of range size and close failure", size: huge, file: &closeTestFile{closeErr: closeErr},
			wantStatus: nfsStatusPointer(NFSStatusInval), wantErrors: []error{os.ErrInvalid, closeErr},
		},
		{
			name: "truncate failure", size: valid, file: &closeTestFile{truncateErr: truncateErr},
			wantErrors: []error{truncateErr}, wantTruncates: 1,
		},
		{
			name: "truncate and close failure", size: valid, file: &closeTestFile{truncateErr: truncateErr, closeErr: closeErr},
			wantErrors: []error{truncateErr, closeErr}, wantTruncates: 1,
		},
		{
			name: "success", size: valid, file: &closeTestFile{}, wantTruncates: 1,
		},
		{
			name: "close failure after truncate", size: valid, file: &closeTestFile{closeErr: closeErr},
			wantErrors: []error{closeErr}, wantTruncates: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := &closeTestFS{file: test.file, info: closeTestInfo{mode: 0o600, size: 16}}
			attrs := SetFileAttributes{SetSize: &test.size}
			err := attrs.Apply(nil, fs, "alpha.txt")
			if test.file.closeCalls != 1 {
				t.Errorf("Close calls = %d, want exactly 1", test.file.closeCalls)
			}
			if test.file.truncateCalls != test.wantTruncates {
				t.Errorf("Truncate calls = %d, want %d", test.file.truncateCalls, test.wantTruncates)
			}
			if len(test.wantErrors) == 0 {
				if err != nil {
					t.Fatalf("Apply error = %v, want success", err)
				}
				if test.file.truncateSize != int64(test.size) {
					t.Errorf("Truncate size = %d, want %d", test.file.truncateSize, test.size)
				}
				return
			}
			if test.wantStatus != nil {
				requireDirectNFSStatus(t, err, *test.wantStatus)
			} else if err == nil {
				t.Fatal("Apply error = nil, want injected failure")
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Errorf("Apply error = %v, want errors.Is(%v)", err, wantErr)
				}
			}
		})
	}
}

func TestSetFileAttributesApplyKeepsChangesBeforeSizeFailure(t *testing.T) {
	mode := uint32(0o644)
	huge := uint64(math.MaxInt64) + 1
	file := &closeTestFile{}
	fs := &closeTestFS{file: file, info: closeTestInfo{mode: 0o600, size: 16}}
	changer := &closeTestChange{info: &fs.info}
	attrs := SetFileAttributes{SetMode: &mode, SetSize: &huge}

	err := attrs.Apply(changer, fs, "alpha.txt")
	requireDirectNFSStatus(t, err, NFSStatusInval)
	if changer.chmodCalls != 1 || fs.info.mode != os.FileMode(mode) {
		t.Errorf("earlier mode change = calls %d, mode %#o; want committed 0644 without rollback", changer.chmodCalls, fs.info.mode)
	}
	if file.truncateCalls != 0 {
		t.Errorf("Truncate calls = %d, want 0 for out-of-range size", file.truncateCalls)
	}
	if file.closeCalls != 1 {
		t.Errorf("Close calls = %d, want exactly 1", file.closeCalls)
	}
}

type closeTestChange struct {
	billy.Change
	info       *closeTestInfo
	chmodCalls int
}

func (c *closeTestChange) Chmod(_ string, mode os.FileMode) error {
	c.chmodCalls++
	c.info.mode = mode
	return nil
}

func nfsStatusPointer(status NFSStatus) *NFSStatus { return &status }

var _ billy.File = (*closeTestFile)(nil)
var _ billy.Filesystem = (*closeTestFS)(nil)
