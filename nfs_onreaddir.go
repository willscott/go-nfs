package nfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type readDirArgs struct {
	Handle      []byte
	Cookie      uint64
	CookieVerif uint64
	Count       uint32
}

type readDirEntity struct {
	FileID uint64
	Name   []byte
	Cookie uint64
	Next   uint32
}

func onReadDir(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = opAttrErrorFormatter
	obj := readDirArgs{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	fs, p, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	contents, err := fs.ReadDir(fs.Join(p...))
	if err != nil {
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
		return &NFSStatusError{NFSStatusNotDir, err}
	}

	if obj.Count < 1024 {
		return &NFSStatusError{NFSStatusTooSmall, io.ErrShortBuffer}
	}

	entities := make([]readDirEntity, 0)
	maxBytes := uint32(100) // conservative overhead measure

	started := (obj.Cookie == 0)
	//calculate the cookieverifier for this read-dir exercise.
	//Note: this is an inefficient way to do this for large directories where
	//paging actually occurs. however, the billy interface doesn't expose the
	//granularity to do better, either.
	vHash := sha256.New()

	for i, c := range contents {
		if started {
			entities = append(entities, readDirEntity{
				FileID: 1337, //todo: does this matter?
				Name:   []byte(c.Name()),
				Cookie: uint64(i + 3),
				Next:   1,
			})
			maxBytes += 512 // TODO: better estimation.
		} else if uint64(i) == obj.Cookie {
			started = true
		} else if maxBytes > obj.Count {
			started = false
			entities = entities[0 : len(entities)-1]
		}
		if _, err := vHash.Write([]byte(c.Name())); err != nil {
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}

	verif := vHash.Sum([]byte{})[0:8]

	if obj.Cookie != 0 && binary.BigEndian.Uint64(verif) != obj.CookieVerif {
		return &NFSStatusError{NFSStatusBadCookie, os.ErrInvalid}
	}

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

	if obj.Cookie == 0 {
		// prefix the special "." and ".." entries.
		if err := xdr.Write(writer, uint32(1)); err != nil { //next
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint64(binary.BigEndian.Uint64(obj.Handle[0:8]))); err != nil { //fileID
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, []byte(".")); err != nil { // name
			return &NFSStatusError{NFSStatusServerFault, err}
		}
		if err := xdr.Write(writer, uint64(1)); err != nil { // cookie
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
		if err := xdr.Write(writer, uint64(2)); err != nil { // cookie
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}
	if len(entities) > 0 || obj.Cookie == 0 {
		if err := xdr.Write(writer, uint32(1)); err != nil { // next
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
	if started || len(entities) == 0 {
		if err := xdr.Write(writer, uint32(1)); err != nil {
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	} else {
		if err := xdr.Write(writer, uint32(0)); err != nil {
			return &NFSStatusError{NFSStatusServerFault, err}
		}
	}
	// TODO: track writer size at this point to validate maxcount estimation and stop early if needed.

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
