package nfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"

	xdr2 "github.com/rasky/go-xdr/xdr2"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func TestReadRequestHeaderAuthLimit(t *testing.T) {
	for _, sizes := range [][2]int{{0, 0}, {1, 3}, {400, 0}, {0, 400}, {400, 400}, {401, 0}, {0, 401}} {
		t.Run(fmt.Sprintf("credential=%d/verifier=%d", sizes[0], sizes[1]), func(t *testing.T) {
			cred := bytes.Repeat([]byte{1}, sizes[0])
			verf := bytes.Repeat([]byte{2}, sizes[1])
			call := struct {
				XID, Type uint32
				Header    rpc.Header
			}{
				XID: 1,
				Header: rpc.Header{
					Rpcvers: 2, Prog: testProgram, Vers: 3, Proc: testProcFast,
					Cred: rpc.Auth{Flavor: 1, Body: cred},
					Verf: rpc.Auth{Flavor: 1, Body: verf},
				},
			}
			var record bytes.Buffer
			if err := xdr.Write(&record, call); err != nil {
				t.Fatal(err)
			}
			// The authentication limit must not consume or limit procedure arguments.
			body := bytes.Repeat([]byte{3}, 1024)
			record.Write(body)
			var marker [4]byte
			binary.BigEndian.PutUint32(marker[:], uint32(record.Len())|1<<31)
			reader := bufio.NewReader(io.MultiReader(bytes.NewReader(marker[:]), &record))
			w, err := (&conn{}).readRequestHeader(context.Background(), reader)
			if sizes[0] > 400 || sizes[1] > 400 {
				if err == nil {
					t.Fatal("accepted an authentication body larger than 400 bytes")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(w.req.Cred.Body, cred) || !bytes.Equal(w.req.Verf.Body, verf) {
				t.Fatal("authentication bodies did not round-trip")
			}
			got, err := io.ReadAll(w.req.Body)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("procedure arguments changed after header decoding: %v", err)
			}
		})
	}
}

func TestReadRequestHeaderRejectsAuthLengthBeforeBody(t *testing.T) {
	for _, field := range []string{"credential", "verifier"} {
		for _, size := range []uint32{401, 1 << 20} {
			t.Run(fmt.Sprintf("%s/%d", field, size), func(t *testing.T) {
				// Advertise a small RPC record and an oversized authentication
				// body, without supplying that body. Reject its length before
				// allocating or trying to read the missing bytes.
				fields := []uint32{40 | 1<<31, 1, 0, 2, testProgram, 3, testProcFast}
				if field == "verifier" {
					fields = append(fields, 0, 0) // empty credential
				}
				fields = append(fields, 1, size)
				var record bytes.Buffer
				if err := binary.Write(&record, binary.BigEndian, fields); err != nil {
					t.Fatal(err)
				}
				_, err := (&conn{}).readRequestHeader(context.Background(), bufio.NewReader(&record))
				var decodeErr *xdr2.UnmarshalError
				if !errors.As(err, &decodeErr) || decodeErr.ErrorCode != xdr2.ErrOverflow {
					t.Fatalf("got %v, want an authentication length error before reading its body", err)
				}
			})
		}
	}
}

func BenchmarkReadRequestHeaderOversizedAuth(b *testing.B) {
	// A 40-byte record that claims a 1 MiB verifier without sending it.
	fields := []uint32{40 | 1<<31, 1, 0, 2, testProgram, 3, testProcFast, 0, 0, 1, 1 << 20}
	var record bytes.Buffer
	if err := binary.Write(&record, binary.BigEndian, fields); err != nil {
		b.Fatal(err)
	}
	data := record.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := (&conn{}).readRequestHeader(context.Background(), bufio.NewReader(bytes.NewReader(data)))
		if err == nil {
			b.Fatal("accepted an oversized authentication body")
		}
	}
}
