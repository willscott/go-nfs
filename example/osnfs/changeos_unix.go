//go:build darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris

package main

import (
	"golang.org/x/sys/unix"
)

func (fs COS) Mknod(path string, mode uint32, major uint32, minor uint32) error {
	dev := unix.Mkdev(major, minor)
	return unix.Mknod(fs.Join(fs.Root(), path), mode, int(dev))
}

func (fs COS) Mkfifo(path string, mode uint32) error {
	return unix.Mkfifo(fs.Join(fs.Root(), path), mode)
}

func (fs COS) Link(path string, link string) error {
	return unix.Link(fs.Join(fs.Root(), path), link)
}

func (fs COS) Socket(path string) error {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	return unix.Bind(fd, &unix.SockaddrUnix{Name: fs.Join(fs.Root(), path)})
}
