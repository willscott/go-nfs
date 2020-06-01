package nfs

import (
	"context"
)

// Backing billy.FS doesn't support creation of
// char, block, socket, or fifo pipe nodes
func onMknod(ctx context.Context, w *response, userHandle Handler) error {
	return &NFSStatusErrorWithWccData{NFSStatusNotSupp}
}
