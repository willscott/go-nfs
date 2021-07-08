package helpers

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs"
)

func NewExportAuthHandler(fs billy.Filesystem) nfs.Handler {
	return &ExportAuthHandler{fs}
}

type ExportAuthHandler struct {
	fs billy.Filesystem
}

type exportfs map[string][]string

func loadExportsFromFile(path string) (result []exportfs) {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("%v", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if []byte(scanner.Text())[0] != '#' {
			var key string
			var item = make(exportfs)
			for i, s := range strings.Fields(scanner.Text()) {
				if i == 0 {
					key = s
					item[key] = []string{}
				} else {
					item[key] = append(item[key], s)
				}
			}
			result = append(result, item)
		}
	}
	return
}

func (h *ExportAuthHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl billy.Filesystem, auths []nfs.AuthFlavor) {
	exports := loadExportsFromFile("/etc/exports")
	dirPath := string(req.Dirpath)

	remoteIP := conn.RemoteAddr().(*net.TCPAddr).IP
	ones, _ := remoteIP.DefaultMask().Size()
	_, ipv4Net, err := net.ParseCIDR(remoteIP.String() + "/" + strconv.Itoa(ones))
	if err != nil {
		log.Println(err)
		status = nfs.MountStatusErrServerFault
		return
	}

	re := regexp.MustCompile(`^(.*)\((.*)\)$`)

	if len(exports) > 0 {
		for _, item := range exports {
			if v, ok := item[dirPath]; ok {
				for _, s := range v {
					if m := re.FindAllStringSubmatch(s, 1); len(m) > 0 {
						if remoteIP.IsLoopback() || m[0][1] == "*" || m[0][1] == ipv4Net.String() {
							status = nfs.MountStatusOk
							hndl = h.fs
							auths = []nfs.AuthFlavor{nfs.AuthFlavorNull}
							return
						}
					}
				}
			}
		}
	}

	status = nfs.MountStatusErrAcces
	return
}

func (h *ExportAuthHandler) Change(fs billy.Filesystem) billy.Change {
	if c, ok := h.fs.(billy.Change); ok {
		return c
	}
	return nil
}

func (h *ExportAuthHandler) FSStat(ctx context.Context, f billy.Filesystem, s *nfs.FSStat) error {
	return nil
}

func (h *ExportAuthHandler) ToHandle(f billy.Filesystem, s []string) []byte {
	return []byte{}
}

func (h *ExportAuthHandler) FromHandle([]byte) (billy.Filesystem, []string, error) {
	return nil, []string{}, nil
}

func (h *ExportAuthHandler) HandleLimit() int {
	return -1
}
