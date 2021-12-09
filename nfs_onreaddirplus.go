package nfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sort"

	"github.com/willscott/go-nfs-client/nfs/xdr"
	"github.com/willscott/go-nfs/filesystem"
)

type readDirPlusArgs struct {
	Handle      []byte
	Cookie      uint64
	CookieVerif uint64
	DirCount    uint32
	MaxCount    uint32
}

type readDirPlusEntity struct {
	FileID        uint64
	Name          []byte
	Cookie        uint64
	HasAttributes uint32
	Attributes    *FileAttribute
	HasHandle     uint32
	Handle        []byte
	Next          uint32
}

func joinPath(parent []string, elements ...string) []string {
	joinedPath := make([]string, 0, len(parent)+len(elements))
	joinedPath = append(joinedPath, parent...)
	joinedPath = append(joinedPath, elements...)
	return joinedPath
}

func onReadDirPlus(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = opAttrErrorFormatter
	obj := readDirPlusArgs{}
	if err := xdr.Read(w.req.Body, &obj); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	fs, p, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	contents, err := filesystem.ReadDir(fs, filesystem.Join(fs, p...))
	if err != nil {
		return &NFSStatusError{NFSStatusNotDir, err}
	}

	sort.Slice(contents, func(i, j int) bool {
		return contents[i].Name() < contents[j].Name()
	})

	// in case of test, nfs-client send:
	// DirCount = 512
	// MaxCount = 4096
	if obj.DirCount < 512 || obj.MaxCount < 4096 {
		return &NFSStatusError{NFSStatusTooSmall, nil}
	}

	entities := make([]readDirPlusEntity, 0)
	dirBytes := uint32(0)
	maxBytes := uint32(100) // conservative overhead measure

	started := (obj.Cookie == 0)
	//calculate the cookieverifier for this read-dir exercise.
	//Note: this is an inefficient way to do this for large directories where
	//paging actually occurs. however, the billy interface doesn't expose the
	//granularity to do better, either.
	vHash := sha256.New()

	for i, c := range contents {
		// index of contents doesn't include '.' and '..'
		actualI := i + 2
		if obj.Cookie > 0 && uint64(actualI) == obj.Cookie+1 {
			verif := vHash.Sum([]byte{})[0:8]
			if binary.BigEndian.Uint64(verif) != obj.CookieVerif {
				return &NFSStatusError{NFSStatusBadCookie, nil}
			}
		}
		if started {
			handle := userHandle.ToHandle(fs, joinPath(p, c.Name()))
			info, err := c.Info()
			if err != nil {
				return &NFSStatusError{NFSStatusServerFault, err}
			}
			attrs := ToFileAttribute(info)
			attrs.Fileid = binary.BigEndian.Uint64(handle[0:8])
			entities = append(entities, readDirPlusEntity{
				FileID:        binary.BigEndian.Uint64(handle[0:8]),
				Name:          []byte(c.Name()),
				Cookie:        uint64(actualI),
				HasAttributes: 1,
				Attributes:    attrs,
				HasHandle:     1,
				Handle:        handle,
				Next:          1,
			})
			dirBytes += uint32(len(c.Name()) + 20)
			maxBytes += 512 // TODO: better estimation.
		} else if uint64(actualI) == obj.Cookie {
			started = true
		}
		if started && (dirBytes > obj.DirCount || maxBytes > obj.MaxCount || len(entities) > userHandle.HandleLimit()/2) {
			started = false
			entities = entities[0 : len(entities)-1]
			break
		}
		if _, err := vHash.Write([]byte(c.Name())); err != nil {
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}

	verif := vHash.Sum([]byte{})[0:8]

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, tryStat(fs, p)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	var fixedVerif [8]byte
	copy(fixedVerif[:], verif)
	if err := xdr.Write(writer, fixedVerif); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if len(entities) > 0 {
		if err := xdr.Write(writer, uint32(1)); err != nil { //next
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}
	if obj.Cookie == 0 {
		// prefix the special "." and ".." entries.
		if err := xdr.Write(writer, uint64(binary.BigEndian.Uint64(obj.Handle[0:8]))); err != nil { //fileID
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, []byte(".")); err != nil { // name
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint64(0)); err != nil { // cookie
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint32(0)); err != nil { // hasAttribute
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint32(0)); err != nil { // hasHandle
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint32(1)); err != nil { // next
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if len(p) > 0 {
			ph := userHandle.ToHandle(fs, p[0:len(p)-1])
			if err := xdr.Write(writer, uint64(binary.BigEndian.Uint64(ph[0:8]))); err != nil { //fileID
				return &NFSStatusError{NFSStatusServerFault, err}
			}
		} else {
			if err := xdr.Write(writer, uint64(0)); err != nil { //fileID
				return &NFSStatusError{NFSStatusServerFault, err}
			}
		}
		if err := xdr.Write(writer, []byte("..")); err != nil { //name
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint64(1)); err != nil { // cookie
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint32(0)); err != nil { // hasAttribute
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint32(0)); err != nil { // hasHandle
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		next := 1
		if len(entities) == 0 {
			next = 0
		}
		if err := xdr.Write(writer, uint32(next)); err != nil { // next
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}
	if len(entities) > 0 {
		entities[len(entities)-1].Next = 0
		// the 'yes there is a 1st entity' bool
	}
	for _, e := range entities {
		if err := xdr.Write(writer, e); err != nil {
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}
	eof := uint32(1)
	if !started {
		eof = 0
	}
	if err := xdr.Write(writer, eof); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	// TODO: track writer size at this point to validate maxcount estimation and stop early if needed.

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
