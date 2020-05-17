package nfs

import (
	"context"
)

const (
	nfsServiceID = 100003
)

func init() {
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureNull), onNull)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureLookup), onLookup)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureGetAttr), onGetAttr)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureAccess), onAccess)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureReadDirPlus), onReadDirPlus)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureFSStat), onFSStat)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedureFSInfo), onFSInfo)
	RegisterMessageHandler(nfsServiceID, uint32(NFSProcedurePathConf), onPathConf)
}

func onNull(ctx context.Context, w *response, userHandle Handler) error {
	return w.Write([]byte{})
}
