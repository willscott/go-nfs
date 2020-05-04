package nfs

// NFSProcedure is the valid RPC calls for the nfs service.
type NFSProcedure uint32

// NfsProcedure Codes
const (
	NFSProcedureNull NFSProcedure = iota
	NFSProcedureGetAttr
	NFSProcedureSetAttr
	NFSProcedureLookup
	NFSProcedureAccess
	NFSProcedureReadlink
	NFSProcedureRead
	NFSProcedureWrite
	NFSProcedureCreate
	NFSProcedureMkDir
	NFSProcedureSymlink
	NFSProcedureMkNod
	NFSProcedureRemove
	NFSProcedureRmDir
	NFSProcedureRename
	NFSProcedureLink
	NFSProcedureReadDir
	NFSProcedureReadDirPlus
	NFSProcedureFSStat
	NFSProcedureFSInfo
	NFSProcedurePathConf
	NFSProcedureCommit
)

func (n NFSProcedure) String() string {
	switch n {
	case NFSProcedureNull:
		return "Null"
	case NFSProcedureGetAttr:
		return "GetAttr"
	case NFSProcedureSetAttr:
		return "SetAttr"
	case NFSProcedureLookup:
		return "Lookup"
	case NFSProcedureAccess:
		return "Access"
	case NFSProcedureReadlink:
		return "ReadLink"
	case NFSProcedureRead:
		return "Read"
	case NFSProcedureWrite:
		return "Write"
	case NFSProcedureCreate:
		return "Create"
	case NFSProcedureMkDir:
		return "Mkdir"
	case NFSProcedureSymlink:
		return "Symlink"
	case NFSProcedureMkNod:
		return "Mknod"
	case NFSProcedureRemove:
		return "Remove"
	case NFSProcedureRmDir:
		return "Rmdir"
	case NFSProcedureRename:
		return "Rename"
	case NFSProcedureLink:
		return "Link"
	case NFSProcedureReadDir:
		return "ReadDir"
	case NFSProcedureReadDirPlus:
		return "ReadDirPlus"
	case NFSProcedureFSStat:
		return "FSStat"
	case NFSProcedureFSInfo:
		return "FSInfo"
	case NFSProcedurePathConf:
		return "PathConf"
	case NFSProcedureCommit:
		return "Commit"
	default:
		return "Unknown"
	}
}
