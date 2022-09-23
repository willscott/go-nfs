//go:build !checksumverifier

package nfs

import (
	"io/fs"
	"sort"

	"github.com/go-git/go-billy/v5"
)

func contentsWithVerifier(fs billy.Filesystem, path string) ([]fs.FileInfo, uint64, error) {
	contents, err := fs.ReadDir(path)
	if err != nil {
		return nil, 0, &NFSStatusError{NFSStatusNotDir, err}
	}

	sort.Slice(contents, func(i, j int) bool {
		return contents[i].Name() < contents[j].Name()
	})

	stat, err := fs.Stat(path)
	if err != nil {
		return nil, 0, &NFSStatusError{NFSStatusServerFault, err}
	}

	verifier := uint64(stat.ModTime().UnixMicro())
	return contents, verifier, nil
}
