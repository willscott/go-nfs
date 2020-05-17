package nfs

import (
	"io"
	"os"
	"syscall"
	"time"

	"github.com/go-git/go-billy/v5"
	nfsc "github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

// FileAttribute handler is expected to fill in
type FileAttribute = nfsc.Fattr

// FileType represents a NFS File Type
type FileType uint32

// Enumeration of NFS FileTypes
const (
	FileTypeRegular FileType = iota + 1
	FileTypeDirectory
	FileTypeBlock
	FileTypeCharacter
	FileTypeLink
	FileTypeSocket
	FileTypeFIFO
)

func (f FileType) String() string {
	switch f {
	case FileTypeRegular:
		return "Regular"
	case FileTypeDirectory:
		return "Directory"
	case FileTypeBlock:
		return "Block Device"
	case FileTypeCharacter:
		return "Character Device"
	case FileTypeLink:
		return "Symbolic Link"
	case FileTypeSocket:
		return "Socket"
	case FileTypeFIFO:
		return "FIFO"
	default:
		return "Unknown"
	}
}

// ToFileAttribute creates an NFS fattr3 struct from an OS.FileInfo
func ToFileAttribute(info os.FileInfo) FileAttribute {
	f := FileAttribute{}

	m := info.Mode()
	f.FileMode = uint32(m)
	if info.IsDir() {
		f.Type = uint32(FileTypeDirectory)
	} else if m&os.ModeSymlink != 0 {
		f.Type = uint32(FileTypeLink)
	} else if m&os.ModeCharDevice != 0 {
		f.Type = uint32(FileTypeCharacter)
		// TODO: set major/minor dev number
		//f.SpecData = 0,0
	} else if m&os.ModeDevice != 0 {
		f.Type = uint32(FileTypeBlock)
		// TODO: set major/minor dev number
		//f.SpecData = 0,0
	} else if m&os.ModeSocket != 0 {
		f.Type = uint32(FileTypeSocket)
	} else if m&os.ModeNamedPipe != 0 {
		f.Type = uint32(FileTypeFIFO)
	} else {
		f.Type = uint32(FileTypeRegular)
	}
	// The number of hard links to the file.
	f.Nlink = 1

	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		f.Nlink = uint32(s.Nlink)
		f.UID = s.Uid
		f.GID = s.Gid
	}

	f.Filesize = uint64(info.Size())
	f.Used = uint64(info.Size())
	f.Atime = ToNFSTime(info.ModTime())
	f.Mtime = f.Atime
	f.Ctime = f.Atime
	return f
}

// WritePostOpAttrs writes the `post_op_attr` representation of a files attributes
func WritePostOpAttrs(writer io.Writer, fs billy.Filesystem, path []string) {
	attrs, err := fs.Stat(fs.Join(path...))
	if err != nil {
		_ = xdr.Write(writer, uint32(0))
	}
	_ = xdr.Write(writer, uint32(1))
	_ = xdr.Write(writer, ToFileAttribute(attrs))
}

// ToNFSTime generates the nfs 64bit time format from a golang time.
func ToNFSTime(t time.Time) nfsc.NFS3Time {
	return nfsc.NFS3Time{
		Seconds:  uint32(t.Unix()),
		Nseconds: uint32(t.UnixNano()) % uint32(time.Second),
	}
}
