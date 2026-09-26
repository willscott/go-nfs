package nfs

import (
	"context"
)

const (
	nfsServiceID = 100003
)

func init() {
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureNull), onNull)               // 0
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureGetAttr), onGetAttr)         // 1
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureSetAttr), onSetAttr)         // 2
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureLookup), onLookup)           // 3
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureAccess), onAccess)           // 4
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureReadlink), onReadLink)       // 5
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureRead), onRead)               // 6
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureWrite), onWrite)             // 7
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureCreate), onCreate)           // 8
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureMkDir), onMkdir)             // 9
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureSymlink), onSymlink)         // 10
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureMkNod), onMknod)             // 11
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureRemove), onRemove)           // 12
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureRmDir), onRmDir)             // 13
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureRename), onRename)           // 14
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureLink), onLink)               // 15
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureReadDir), onReadDir)         // 16
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureReadDirPlus), onReadDirPlus) // 17
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureFSStat), onFSStat)           // 18
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureFSInfo), onFSInfo)           // 19
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedurePathConf), onPathConf)       // 20
	_ = RegisterVersionedMessageHandler(nfsServiceID, 3, uint32(NFSProcedureCommit), onCommit)           // 21
}

func onNull(ctx context.Context, w *response, userHandle Handler) error {
	return w.Write([]byte{})
}
