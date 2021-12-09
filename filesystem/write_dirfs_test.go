package filesystem_test

import (
	"io/fs"
	"testing"

	"github.com/willscott/go-nfs/filesystem"
)

func TestCheckInterfacesDirWrapperFS(t *testing.T) {
	dfs := filesystem.NewWriteDirFSWrapper(".")

	// Methods that can be substituted by wrapper fs.
	if _, ok := dfs.(filesystem.JoinFS); !ok {
		t.Errorf("os.DirFS should implement JoinFS")
	}

	if _, ok := dfs.(fs.StatFS); !ok {
		t.Errorf("os.DirFS should implement StatFS")
	}

	if _, ok := dfs.(fs.ReadDirFS); !ok {
		t.Errorf("os.DirFS should implement ReadDirFS")
	}

	if _, ok := dfs.(filesystem.WriteFileFS); !ok {
		t.Errorf("os.DirFS should implement WriteFileFS")
	}

	if _, ok := dfs.(filesystem.MkdirAllFS); !ok {
		t.Errorf("os.DirFS should implement MkdirAll")
	}

	if _, ok := dfs.(filesystem.CreateFS); !ok {
		t.Errorf("os.DirFS should implement CreateFS")
	}

	if _, ok := dfs.(filesystem.ReadlinkFS); !ok {
		t.Errorf("os.DirFS should implement ReadlinkFS")
	}

	if _, ok := dfs.(filesystem.RemoveFS); !ok {
		t.Errorf("os.DirFS should implement RemoveFS")
	}

	if _, ok := dfs.(filesystem.RenameFS); !ok {
		t.Errorf("os.DirFS should implement RenameFS")
	}

	if _, ok := dfs.(filesystem.LstatFS); !ok {
		t.Errorf("os.DirFS should implement LstatFS")
	}

	if _, ok := dfs.(filesystem.SymlinkFS); !ok {
		t.Errorf("os.DirFS should implement SymlinkFS")
	}
}

func TestCheckInterfacesDirWithChangeWrapper(t *testing.T) {
	dfsc := filesystem.NewWriteDirFSWithChangeWrapper(".")

	if _, ok := dfsc.(filesystem.ChmodFS); !ok {
		t.Errorf("os.DirFS should implement ChmodFS")
	}

	if _, ok := dfsc.(filesystem.LchownFS); !ok {
		t.Errorf("os.DirFS should implement LchownFS")
	}

	if _, ok := dfsc.(filesystem.ChownFS); !ok {
		t.Errorf("os.DirFS should implement ChownFS")
	}

	if _, ok := dfsc.(filesystem.ChtimesFS); !ok {
		t.Errorf("os.DirFS should implement ChtimesFS")
	}
}

func TestCheckInterfacesDirWrapperFile(t *testing.T) {
	dfs := filesystem.NewWriteDirFSWrapper(".")
	file, err := dfs.Open("fs_test.go")
	if err != nil {
		t.Errorf("os.DirFS.Open: %v", err)
		return
	}

	// Methods that can be substituted by file.
	if _, ok := file.(filesystem.ReadAtFile); !ok {
		t.Errorf("os.DirFS should implement ReadAtFile")
	}

	if _, ok := file.(filesystem.SeekFile); !ok {
		t.Errorf("os.DirFS should implement SeekFile")
	}

	if _, ok := file.(filesystem.WriteAtFile); !ok {
		t.Errorf("os.DirFS should implement WriteAtFile")
	}

	if _, ok := file.(filesystem.TruncateFile); !ok {
		t.Errorf("os.DirFS should implement TruncateFile")
	}

	if _, ok := file.(filesystem.WriteFile); !ok {
		t.Errorf("os.DirFS should implement WriteFile")
	}
}
