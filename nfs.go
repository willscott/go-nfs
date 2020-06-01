package nfs

import (
	"context"
)

const (
	nfsServiceID = 100003
)

func init() {
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureNull), onNull)         // 0
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureGetAttr), onGetAttr)   // 1
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureSetAttr), onSetAttr)   // 2
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureLookup), onLookup)     // 3
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureAccess), onAccess)     // 4
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureReadlink), onReadLink) // 5
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureRead), onRead)         // 6
	// Write // 7
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureCreate), onCreate)   // 8
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureMkDir), onMkdir)     // 9
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureSymlink), onSymlink) // 10
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureMkNod), onMknod)     // 11
	// Remove // 12
	// Rmdir // 13
	// Rename // 14
	// Link // 15
	// ReadDir // 16
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureReadDirPlus), onReadDirPlus) // 17
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureFSStat), onFSStat)           // 18
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureFSInfo), onFSInfo)           // 19
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedurePathConf), onPathConf)       // 20
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureCommit), onCommit)           // 21
}

func onNull(ctx context.Context, w *response, userHandle Handler) error {
	return w.Write([]byte{})
}
