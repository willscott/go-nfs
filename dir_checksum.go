//go:build checksumverifier

package nfs

import (
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"sort"

	"github.com/go-git/go-billy/v5"
)

func checksumVerifier(contents []fs.FileInfo) uint64 {
	//calculate the cookie-verifier for this read-dir exercise.
	//Note: this is an inefficient way to do this for large directories where
	//paging actually occurs. however, the billy interface doesn't expose the
	//granularity to do better, either.
	vHash := sha256.New()

	for _, c := range contents {
		vHash.Write([]byte(c.Name())) // Never fails according to the docs
	}

	verify := vHash.Sum(nil)[0:8]
	return binary.BigEndian.Uint64(verify)
}

func contentsWithVerifier(fs billy.Filesystem, path string) ([]fs.FileInfo, uint64, error) {
	contents, err := fs.ReadDir(path)
	if err != nil {
		return nil, 0, &NFSStatusError{NFSStatusNotDir, err}
	}

	sort.Slice(contents, func(i, j int) bool {
		return contents[i].Name() < contents[j].Name()
	})

	verifier := checksumVerifier(contents)
	return contents, verifier, nil
}
