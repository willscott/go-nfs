//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !nacl && !netbsd && !openbsd && !solaris && !wasm

package file

import (
	"os"
)

func getOSFileInfo(_ os.FileInfo) *FileInfo {
	return nil
}
