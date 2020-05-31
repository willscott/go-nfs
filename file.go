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

// FromNFSTime generates a golang time from an nfs time spec
func FromNFSTime(t nfsc.NFS3Time) *time.Time {
	out := time.Unix(int64(t.Seconds), int64(t.Nseconds))
	return &out
}

// SetFileAttributes represents a command to update some metadata
// about a file.
type SetFileAttributes struct {
	SetMode  *uint32
	SetUID   *uint32
	SetGID   *uint32
	SetSize  *uint64
	SetAtime *time.Time
	SetMtime *time.Time
}

// Apply uses a `Change` implementation to set defined attributes on a
// provided file.
func (s *SetFileAttributes) Apply(changer billy.Change, fs billy.Filesystem, file string) error {
	cur := func() *FileAttribute {
		curOS, err := fs.Lstat(file)
		if err != nil {
			return nil
		}
		curr := ToFileAttribute(curOS)
		return &curr
	}

	if s.SetMode != nil {
		mode := os.FileMode(*s.SetMode) & os.ModePerm
		if err := changer.Chmod(file, mode); err != nil {
			return err
		}
	}
	if s.SetUID != nil || s.SetGID != nil {
		curr := cur()
		euid := curr.UID
		if s.SetUID != nil {
			euid = *s.SetUID
		}
		egid := curr.GID
		if s.SetGID != nil {
			egid = *s.SetGID
		}
		if err := changer.Chown(file, int(euid), int(egid)); err != nil {
			return err
		}
	}
	if s.SetSize != nil {
		fp, err := fs.Open(file)
		if err != nil {
			return err
		}
		if err := fp.Truncate(int64(*s.SetSize)); err != nil {
			return err
		}
		if err := fp.Close(); err != nil {
			return err
		}
	}

	if s.SetAtime != nil || s.SetMtime != nil {
		curr := cur()
		atime := FromNFSTime(curr.Atime)
		if s.SetAtime != nil {
			atime = s.SetAtime
		}
		mtime := FromNFSTime(curr.Mtime)
		if s.SetMtime != nil {
			mtime = s.SetMtime
		}
		if err := changer.Chtimes(file, *atime, *mtime); err != nil {
			return err
		}
	}
	return nil
}

// ReadSetFileAttributes reads an sattr3 xdr stream into a go struct.
func ReadSetFileAttributes(r io.Reader) (*SetFileAttributes, error) {
	attrs := SetFileAttributes{}
	hasMode, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if hasMode != 0 {
		mode, err := xdr.ReadUint32(r)
		if err != nil {
			return nil, err
		}
		attrs.SetMode = &mode
	}
	hasUID, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if hasUID != 0 {
		uid, err := xdr.ReadUint32(r)
		if err != nil {
			return nil, err
		}
		attrs.SetUID = &uid
	}
	hasGID, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if hasGID != 0 {
		gid, err := xdr.ReadUint32(r)
		if err != nil {
			return nil, err
		}
		attrs.SetGID = &gid
	}
	hasSize, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if hasSize != 0 {
		var size uint64
		attrs.SetSize = &size
		if err := xdr.Read(r, size); err != nil {
			return nil, err
		}
	}
	aTime, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if aTime == 1 {
		now := time.Now()
		attrs.SetAtime = &now
	} else if aTime == 2 {
		t := nfsc.NFS3Time{}
		if err := xdr.Read(r, t); err != nil {
			return nil, err
		}
		attrs.SetAtime = FromNFSTime(t)
	}
	mTime, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	if mTime == 1 {
		now := time.Now()
		attrs.SetMtime = &now
	} else if mTime == 2 {
		t := nfsc.NFS3Time{}
		if err := xdr.Read(r, t); err != nil {
			return nil, err
		}
		attrs.SetMtime = FromNFSTime(t)
	}
	return &attrs, nil
}
