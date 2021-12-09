package nfs_test

import (
	"testing"
)

func TestNFS(t *testing.T) {
	// // make an empty in-memory server.
	// listener, err := net.Listen("tcp", "localhost:0")
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// mem := memfs.New()

	// handler := helpers.NewNullAuthHandler(mem)
	// cacheHelper := helpers.NewCachingHandler(handler, 1024)
	// go func() {
	// 	_ = nfs.Serve(listener, cacheHelper)
	// }()

	// c, err := rpc.DialTCP(listener.Addr().Network(), nil, listener.Addr().(*net.TCPAddr).String())
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// defer c.Close()

	// var mounter nfsc.Mount
	// mounter.Client = c
	// target, err := mounter.Mount("/", rpc.AuthNull)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// defer func() {
	// 	_ = mounter.Unmount()
	// }()

	// _, err = target.FSInfo()
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// // Validate sample file creation
	// _, err = target.Create("/helloworld.txt", 0666)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// if info, err := mem.Stat("/helloworld.txt"); err != nil {
	// 	t.Fatal(err)
	// } else {
	// 	if info.Size() != 0 || info.Mode().Perm() != 0666 {
	// 		t.Fatal("incorrect creation.")
	// 	}
	// }

	// // Validate writing to a file.
	// f, err := target.OpenFile("/helloworld.txt", 0666)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// b := []byte("hello world")
	// _, err = f.Write(b)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// mf, _ := mem.Open("/helloworld.txt")
	// buf := make([]byte, len(b))
	// if _, err = mf.Read(buf[:]); err != nil {
	// 	t.Fatal(err)
	// }
	// if !bytes.Equal(buf, b) {
	// 	t.Fatal("written does not match expected")
	// }

	// // for test nfs.ReadDirPlus in case of many files
	// dirF1, err := mem.ReadDir("/")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// shouldBeNames := []string{".", ".."}
	// for _, f := range dirF1 {
	// 	shouldBeNames = append(shouldBeNames, f.Name())
	// }
	// for i := 0; i < 100; i++ {
	// 	fName := fmt.Sprintf("f-%03d.txt", i)
	// 	shouldBeNames = append(shouldBeNames, fName)
	// 	f, err := mem.Create(fName)
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}
	// 	f.Close()
	// }

	// entities, err := target.ReadDirPlus("/")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// actualBeNames := []string{}
	// for _, e := range entities {
	// 	actualBeNames = append(actualBeNames, e.Name())
	// }

	// as := sort.StringSlice(shouldBeNames)
	// bs := sort.StringSlice(actualBeNames)
	// as.Sort()
	// bs.Sort()
	// if !reflect.DeepEqual(as, bs) {
	// 	t.Fatal("nfs.ReadDirPlus error")
	// }
}
