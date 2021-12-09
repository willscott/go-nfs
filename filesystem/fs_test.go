package filesystem_test

// THESE TEST ARE DESIGNED TO FAIL

// import (
// 	"io/fs"
// 	"os"
// 	"testing"

// 	"github.com/willscott/go-nfs/filesystem"
// )

// func TestCheckInterfacesDirOS(t *testing.T) {
// 	dfs := os.DirFS(".")

// 	// Methods that can be substituted by fs.
// 	if _, ok := dfs.(filesystem.JoinFS); !ok {
// 		t.Errorf("os.DirFS should implement JoinFS")
// 	}

// 	if _, ok := dfs.(fs.StatFS); !ok {
// 		t.Errorf("os.DirFS should implement StatFS")
// 	}

// 	if _, ok := dfs.(fs.ReadDirFS); !ok {
// 		t.Errorf("os.DirFS should implement ReadDirFS")
// 	}

// 	if _, ok := dfs.(filesystem.WriteFileFS); !ok {
// 		t.Errorf("os.DirFS should implement WriteFileFS")
// 	}

// 	if _, ok := dfs.(filesystem.MkdirAllFS); !ok {
// 		t.Errorf("os.DirFS should implement MkdirAll")
// 	}

// 	if _, ok := dfs.(filesystem.CreateFS); !ok {
// 		t.Errorf("os.DirFS should implement CreateFS")
// 	}

// 	if _, ok := dfs.(filesystem.ChmodFS); !ok {
// 		t.Errorf("os.DirFS should implement ChmodFS")
// 	}

// 	if _, ok := dfs.(filesystem.LchownFS); !ok {
// 		t.Errorf("os.DirFS should implement LchownFS")
// 	}

// 	if _, ok := dfs.(filesystem.ChownFS); !ok {
// 		t.Errorf("os.DirFS should implement ChownFS")
// 	}

// 	if _, ok := dfs.(filesystem.ChtimesFS); !ok {
// 		t.Errorf("os.DirFS should implement ChtimesFS")
// 	}

// 	if _, ok := dfs.(filesystem.ReadlinkFS); !ok {
// 		t.Errorf("os.DirFS should implement ReadlinkFS")
// 	}

// 	if _, ok := dfs.(filesystem.RemoveFS); !ok {
// 		t.Errorf("os.DirFS should implement RemoveFS")
// 	}

// 	if _, ok := dfs.(filesystem.RenameFS); !ok {
// 		t.Errorf("os.DirFS should implement RenameFS")
// 	}

// 	if _, ok := dfs.(filesystem.LstatFS); !ok {
// 		t.Errorf("os.DirFS should implement LstatFS")
// 	}

// 	if _, ok := dfs.(filesystem.SymlinkFS); !ok {
// 		t.Errorf("os.DirFS should implement SymlinkFS")
// 	}

// }

// func TestCheckInterfacesFile(t *testing.T) {
// 	dfs := os.DirFS(".")
// 	file, err := dfs.Open("fs_test.go")
// 	if err != nil {
// 		t.Errorf("os.DirFS.Open: %w", err)
// 		return
// 	}

// 	// Methods that can be substituted by file.
// 	if _, ok := file.(filesystem.ReadAtFile); !ok {
// 		t.Errorf("os.DirFS should implement ReadAtFile")
// 	}

// 	if _, ok := dfs.(filesystem.SeekFile); !ok {
// 		t.Errorf("os.DirFS should implement SeekFile")
// 	}

// 	if _, ok := dfs.(filesystem.WriteAtFile); !ok {
// 		t.Errorf("os.DirFS should implement WriteAtFile")
// 	}

// 	if _, ok := dfs.(filesystem.TruncateFile); !ok {
// 		t.Errorf("os.DirFS should implement TruncateFile")
// 	}

// 	if _, ok := dfs.(filesystem.WriteFile); !ok {
// 		t.Errorf("os.DirFS should implement WriteFile")
// 	}
// }
