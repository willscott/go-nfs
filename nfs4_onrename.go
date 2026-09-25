package nfs

import "io"

type nfs4RenameArgs struct {
	OldName string
	NewName string
}

type nfs4RenameRes struct {
	Source nfs4ChangeInfo
	Target nfs4ChangeInfo
}

// nfs4OnRename moves OldName in the saved directory to NewName in the
// current one.
func nfs4OnRename(c *nfs4Compound, args io.Reader, res io.Writer) nfs4Status {
	var req nfs4RenameArgs
	if status := nfs4Decode(args, &req); status != nfs4OK {
		return status
	}
	if c.saved == nil {
		return nfs4ErrNoFileHandle
	}
	source := c.saved
	target, status := c.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if status := nfs4Component(req.OldName); status != nfs4OK {
		return status
	}
	if status := nfs4Component(req.NewName); status != nfs4OK {
		return status
	}
	if status := source.ensureDir(); status != nfs4OK {
		return status
	}
	if status := target.ensureDir(); status != nfs4OK {
		return status
	}

	// The client may still hold the file's handle, which names it by its
	// old path; invalidated, it answers NFS4ERR_STALE and the client looks
	// the new name up, as NFSv3 RENAME does.
	oldHandle := c.handler.ToHandle(source.fs, source.child(req.OldName))
	sourceBefore := source.changeID()
	targetBefore := target.changeID()
	oldPath := nfs4Join(source.fs, source.child(req.OldName))
	newPath := nfs4Join(target.fs, target.child(req.NewName))
	if err := source.fs.Rename(oldPath, newPath); err != nil {
		return nfs4StatusFromErr(err)
	}
	if err := c.handler.InvalidateHandle(source.fs, oldHandle); err != nil {
		return nfs4ErrServerFault
	}
	return nfs4Encode(res, nfs4RenameRes{
		Source: nfs4ChangeInfo{Before: sourceBefore, After: source.changeID()},
		Target: nfs4ChangeInfo{Before: targetBefore, After: target.changeID()},
	})
}
