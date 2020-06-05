package nfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"

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
		return err
	}

	fs, p, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale}
	}

	contents, err := fs.ReadDir(fs.Join(p...))
	if err != nil {
		return &NFSStatusError{NFSStatusNotDir}
	}

	if obj.Count < 1024 {
		return &NFSStatusError{NFSStatusTooSmall}
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
		_, _ = vHash.Write([]byte(c.Name()))
	}

	verif := vHash.Sum([]byte{})[0:8]

	if obj.Cookie != 0 && binary.BigEndian.Uint64(verif) != obj.CookieVerif {
		return &NFSStatusError{NFSStatusBadCookie}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, tryStat(fs, p))

	var fixedVerif [8]byte
	copy(fixedVerif[:], verif)
	if err := xdr.Write(writer, fixedVerif); err != nil {
		return err
	}

	if obj.Cookie == 0 {
		// prefix the special "." and ".." entries.
		xdr.Write(writer, uint32(1))                                        //next
		xdr.Write(writer, uint64(binary.BigEndian.Uint64(obj.Handle[0:8]))) //fileID
		xdr.Write(writer, []byte("."))                                      // name
		xdr.Write(writer, uint64(1))                                        // cookie
		xdr.Write(writer, uint32(1))                                        // next
		if len(p) > 0 {
			ph := userHandle.ToHandle(fs, p[0:len(p)-1])
			xdr.Write(writer, uint64(binary.BigEndian.Uint64(ph[0:8]))) //fileID
		} else {
			xdr.Write(writer, uint64(0)) //fileID
		}
		xdr.Write(writer, []byte("..")) //name
		xdr.Write(writer, uint64(2))    // cookie
	}
	if len(entities) > 0 || obj.Cookie == 0 {
		xdr.Write(writer, uint32(1)) // next
	}
	if len(entities) > 0 {
		entities[len(entities)-1].Next = 0
		// the 'yes there is a 1st entity' bool
	}
	for _, e := range entities {
		xdr.Write(writer, e)
	}
	if started || len(entities) == 0 {
		xdr.Write(writer, uint32(1))
	} else {
		xdr.Write(writer, uint32(0))
	}
	// TODO: track writer size at this point to validate maxcount estimation and stop early if needed.
	return w.Write(writer.Bytes())
}
