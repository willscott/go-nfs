package nfs

import (
	"context"
	"testing"
)

func TestHandlerForVersions(t *testing.T) {
	const anyProg, versionedProg = 0x20000101, 0x20000102
	unversioned := func(context.Context, *response, Handler) error { return nil }
	v2 := func(context.Context, *response, Handler) error { return nil }
	if err := RegisterMessageHandler(anyProg, 1, unversioned); err != nil {
		t.Fatal(err)
	}
	if err := RegisterVersionedMessageHandler(anyProg, 2, 1, v2); err == nil {
		t.Fatal("a versioned handler over an unversioned one registered, want already registered")
	}
	if err := RegisterVersionedMessageHandler(versionedProg, 2, 1, v2); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMessageHandler(versionedProg, 1, unversioned); err == nil {
		t.Fatal("an unversioned handler over a versioned one registered, want already registered")
	}

	s := &Server{}
	for _, version := range []uint32{1, 3, 4} {
		if h, _ := s.handlerFor(anyProg, version, 1); h == nil {
			t.Errorf("RegisterMessageHandler handler not found for version %d", version)
		}
	}
	if _, err := s.handlerFor(anyProg, 1, 9); err.Code() != ResponseCodeProcUnavailable {
		t.Errorf("unknown procedure: %v, want PROC_UNAVAIL", err)
	}
	if _, err := s.handlerFor(versionedProg, 3, 1); err == nil || *err.(*ProgMismatchError) != (ProgMismatchError{Low: 2, High: 2}) {
		t.Errorf("unserved version: %v, want PROG_MISMATCH 2-2", err)
	}
}

func TestHandlerForNFSVersions(t *testing.T) {
	s := &Server{}
	if h, _ := s.handlerFor(nfsServiceID, 3, uint32(NFSProcedureGetAttr)); h == nil {
		t.Fatal("NFSv3 GETATTR not served")
	}
	if _, err := s.handlerFor(nfsServiceID, 2, uint32(NFSProcedureGetAttr)); err == nil || *err.(*ProgMismatchError) != (ProgMismatchError{Low: 3, High: 4}) {
		t.Fatalf("NFSv2: %v, want PROG_MISMATCH 3-4", err)
	}
	if _, err := s.handlerFor(mountServiceID, 1, uint32(MountProcMount)); err == nil || *err.(*ProgMismatchError) != (ProgMismatchError{Low: 3, High: 3}) {
		t.Fatalf("MOUNTv1: %v, want PROG_MISMATCH 3-3", err)
	}

	s.EnabledNFSVersions = []uint32{3}
	if _, err := s.handlerFor(nfsServiceID, 4, nfs4ProcCompound); err == nil || *err.(*ProgMismatchError) != (ProgMismatchError{Low: 3, High: 3}) {
		t.Fatalf("disabled NFSv4: %v, want PROG_MISMATCH 3-3", err)
	}
}
