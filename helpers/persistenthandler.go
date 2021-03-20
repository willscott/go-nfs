package helpers

import (
	"github.com/dgraph-io/badger"
	"github.com/go-git/go-billy/v5"
	"github.com/google/uuid"
	"github.com/willscott/go-nfs"
	"math"
	"path/filepath"
	"strings"
)

const (
	pathSeparator       = string(filepath.Separator)
	rootPathPlaceholder = "/"
)

type persistentHandler struct {
	nfs.Handler
	fs     billy.Filesystem
	db     *badger.DB // stores both ways: path <---> fileHandleId , both as []byte
	logger badger.Logger
}

var _ nfs.Handler = &persistentHandler{}

func NewPersistentHandler(
	handler nfs.Handler,
	fs billy.Filesystem,
	dataPath string,
	logger badger.Logger,
) (nfs.Handler, error) {
	opts := badger.DefaultOptions(dataPath)
	opts.Logger = logger
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	return &persistentHandler{
		Handler: handler,
		db:      db,
		fs:      fs,
		logger:  logger,
	}, nil
}

func newFileHandle() ([]byte, error) {
	return uuid.New().MarshalBinary()
}

func getDbItem(txn *badger.Txn, key []byte) (*badger.Item, error) {
	item, err := txn.Get(key)
	if err != nil {
		switch err {
		case badger.ErrKeyNotFound:
			return nil, nil
		default:
			return nil, err
		}
	}
	return item, nil
}

func getValueFromDbItem(item *badger.Item) (value []byte, err error) {
	err = item.Value(func(storedValue []byte) error {
		value = make([]byte, len(storedValue))
		copy(value, storedValue)
		return nil
	})
	return
}

func (handler *persistentHandler) ToHandle(_ billy.Filesystem, path []string) []byte {
	fullPath := filepath.Join(path...)
	if len(fullPath) == 0 {
		fullPath = rootPathPlaceholder
	}

	pathBytes := []byte(fullPath)
	var handle []byte
	err := handler.db.Update(func(txn *badger.Txn) error {
		item, err := getDbItem(txn, pathBytes)
		if err != nil {
			return err
		}

		if item != nil {
			handle, err = getValueFromDbItem(item)
			return err
		}

		handle, err = newFileHandle()
		if err != nil {
			return err
		}

		err = txn.Set(pathBytes, handle)
		if err != nil {
			return err
		}

		err = txn.Set(handle, pathBytes)
		return err
	})

	if err != nil || handle == nil {
		if handler.logger != nil {
			handler.logger.Errorf("persistenthandler.ToHandle: failed for '%v': %v", fullPath, err)
		}
		return nil
	}
	return handle
}

func (handler *persistentHandler) FromHandle(handle []byte) (fs billy.Filesystem, path []string, err error) {
	fs = handler.fs

	var fullPath []byte
	err = handler.db.View(func(txn *badger.Txn) error {
		item, err := getDbItem(txn, handle)
		if err != nil || item == nil {
			return err
		}
		fullPath, err = getValueFromDbItem(item)
		return err
	})

	if err != nil || fullPath == nil {
		if handler.logger != nil {
			handler.logger.Errorf("persistenthandler.FromHandle: could not resolve handle '%v': %v", handle, err)
		}
		return nil, []string{}, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
	}

	fullPathStr := string(fullPath)
	if fullPathStr == rootPathPlaceholder {
		path = []string{""}
	} else {
		path = strings.Split(fullPathStr, pathSeparator)
	}
	return
}

func (handler persistentHandler) HandleLimit() int {
	return math.MaxInt32
}
