package file

import (
	"os"
)

// getOSFileInfo returns file information on Unix.
// There are no such fields on Plan 9 -- at least
// of the Unix type. Plan 9 uids and gids are strings.
func getOSFileInfo(info os.FileInfo) *FileInfo {
	return nil
}
