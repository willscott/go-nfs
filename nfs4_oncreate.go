package nfs

import "io"

// nfs4CreateArgs is a CREATE4args. nfs_ftype4 numbers file types as
// ftype3 does, so the FileType constants name them; the union cases are
// NF4BLK, NF4CHR and NF4LNK.
type nfs4CreateArgs struct {
	Type     FileType  `xdr:"union"`
	BlkData  [2]uint32 `xdr:"unioncase=3"`
	ChrData  [2]uint32 `xdr:"unioncase=4"`
	LinkData string    `xdr:"unioncase=5"`
	Name     string
	Attrs    nfs4FAttr
}

type nfs4CreateRes struct {
	CInfo   nfs4ChangeInfo
	AttrSet nfs4Bitmap
}

// nfs4OnCreate makes directories. Other file types are not supported.
func nfs4OnCreate(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4CreateArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	parent, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	switch req.Type {
	case FileTypeDirectory:
	case FileTypeLink, FileTypeBlock, FileTypeCharacter, FileTypeSocket, FileTypeFIFO:
		return nfs4ErrNotSupp
	default:
		return nfs4ErrBadType
	}
	if status := nfs4Component(req.Name); status != nfs4OK {
		return status
	}
	attrs, status := nfs4DecodeSetAttrs(req.Attrs)
	if status != nfs4OK {
		return status
	}
	if status := parent.ensureDir(); status != nfs4OK {
		return status
	}

	childPath := parent.child(req.Name)
	before := parent.changeID()
	if err := parent.fs.MkdirAll(nfs4Join(parent.fs, childPath), attrs.attrs.Mode(0777)); err != nil {
		return nfs4StatusFromErr(err)
	}
	after := parent.changeID()
	if status := c.setCurrent(parent.fs, childPath); status != nfs4OK {
		return status
	}
	return nfs4Encode(res, nfs4CreateRes{
		CInfo:   nfs4ChangeInfo{Before: before, After: after},
		AttrSet: attrs.mask,
	})
}
