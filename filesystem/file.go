package filesystem

import (
	"fmt"
	"io/fs"
)

func ReadAt(file fs.File, b []byte, off int64) (n int, err error) {
	if File, ok := file.(ReadAtFile); ok {
		return File.ReadAt(b, off)
	}

	return 0, fmt.Errorf("readat: operation not supported")
}

func Seek(file fs.File, offset int64, whence int) (int64, error) {
	if File, ok := file.(SeekFile); ok {
		return File.Seek(offset, whence)
	}

	return 0, fmt.Errorf("seek: operation not supported")
}

func WriteAt(file fs.File, b []byte, offset int64, n int) (int, error) {
	if File, ok := file.(WriteAtFile); ok {
		return File.WriteAt(b, offset)
	}

	return 0, fmt.Errorf("writeat: operation not supported")
}

func Write(file fs.File, b []byte) (int, error) {
	if File, ok := file.(WriteFile); ok {
		return File.Write(b)
	}

	return 0, fmt.Errorf("write: operation not supported")
}

func Truncate(file fs.File, size int64) error {
	if File, ok := file.(TruncateFile); ok {
		return File.Truncate(size)
	}

	return fmt.Errorf("truncate: operation not supported")
}
