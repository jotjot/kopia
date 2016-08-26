// Package filesystem implements filesystem-based Storage.
package filesystem

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kopia/kopia/storage"
)

const (
	fsStorageType        = "filesystem"
	fsStorageChunkSuffix = ".f"
)

var (
	fsDefaultShards               = []int{3, 3}
	fsDefaultFileMode os.FileMode = 0664
	fsDefaultDirMode  os.FileMode = 0775
)

type fsStorage struct {
	Options
}

func (fs *fsStorage) BlockSize(blockID string) (int64, error) {
	_, path := fs.getShardedPathAndFilePath(blockID)
	s, err := os.Stat(path)
	if err == nil {
		return s.Size(), nil
	}

	if os.IsNotExist(err) {
		return 0, storage.ErrBlockNotFound
	}

	return 0, err
}

func (fs *fsStorage) GetBlock(blockID string) ([]byte, error) {
	_, path := fs.getShardedPathAndFilePath(blockID)
	d, err := ioutil.ReadFile(path)
	if err == nil {
		return d, err
	}

	if os.IsNotExist(err) {
		return nil, storage.ErrBlockNotFound
	}

	return nil, err
}

func getstringFromFileName(name string) (string, bool) {
	if strings.HasSuffix(name, fsStorageChunkSuffix) {
		return string(name[0 : len(name)-len(fsStorageChunkSuffix)]), true
	}

	return string(""), false
}

func makeFileName(blockID string) string {
	return string(blockID) + fsStorageChunkSuffix
}

func (fs *fsStorage) ListBlocks(prefix string) chan (storage.BlockMetadata) {
	result := make(chan (storage.BlockMetadata))

	prefixString := string(prefix)

	var walkDir func(string, string)

	walkDir = func(directory string, currentPrefix string) {
		if entries, err := ioutil.ReadDir(directory); err == nil {
			//log.Println("Walking", directory, "looking for", prefix)

			for _, e := range entries {
				if e.IsDir() {
					newPrefix := currentPrefix + e.Name()
					var match bool

					if len(prefixString) > len(newPrefix) {
						match = strings.HasPrefix(prefixString, newPrefix)
					} else {
						match = strings.HasPrefix(newPrefix, prefixString)
					}

					if match {
						walkDir(directory+"/"+e.Name(), currentPrefix+e.Name())
					}
				} else if fullID, ok := getstringFromFileName(currentPrefix + e.Name()); ok {
					if strings.HasPrefix(string(fullID), prefixString) {
						result <- storage.BlockMetadata{
							BlockID:   fullID,
							Length:    e.Size(),
							TimeStamp: e.ModTime(),
						}
					}
				}
			}
		}
	}

	walkDirAndClose := func(directory string) {
		walkDir(directory, "")
		close(result)
	}

	go walkDirAndClose(fs.Path)
	return result
}

func (fs *fsStorage) PutBlock(blockID string, data []byte, options storage.PutOptions) error {
	shardPath, path := fs.getShardedPathAndFilePath(blockID)

	// Open temporary file, create dir if required.
	tempFile := fmt.Sprintf("%s.tmp.%d", path, rand.Int())
	flags := os.O_CREATE | os.O_WRONLY | os.O_EXCL
	f, err := os.OpenFile(tempFile, flags, fs.fileMode())
	if os.IsNotExist(err) {
		if err = os.MkdirAll(shardPath, fs.dirMode()); err != nil {
			return fmt.Errorf("cannot create directory: %v", err)
		}
		f, err = os.OpenFile(tempFile, flags, fs.fileMode())
	}

	if err != nil {
		return fmt.Errorf("cannot create temporary file: %v", err)
	}

	f.Write(data)
	f.Close()

	err = os.Rename(tempFile, path)
	if err != nil {
		os.Remove(tempFile)
		return err
	}

	if fs.FileUID != nil && fs.FileGID != nil && os.Geteuid() == 0 {
		os.Chown(path, *fs.FileUID, *fs.FileGID)
	}

	return nil
}

func (fs *fsStorage) DeleteBlock(blockID string) error {
	_, path := fs.getShardedPathAndFilePath(blockID)
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}

	return err
}

func (fs *fsStorage) getShardDirectory(blockID string) (string, string) {
	shardPath := fs.Path
	blockIDString := string(blockID)
	if len(blockIDString) < 20 {
		return shardPath, blockID
	}
	for _, size := range fs.shards() {
		shardPath = filepath.Join(shardPath, blockIDString[0:size])
		blockIDString = blockIDString[size:]
	}

	return shardPath, string(blockIDString)
}

func (fs *fsStorage) getShardedPathAndFilePath(blockID string) (string, string) {
	shardPath, blockID := fs.getShardDirectory(blockID)
	result := filepath.Join(shardPath, makeFileName(blockID))
	return shardPath, result
}

func parseShardString(shardString string) ([]int, error) {
	if shardString == "" {
		// By default Xabcdefghijklmnop is stored in 'X/abc/def/Xabcdefghijklmnop'
		return fsDefaultShards, nil
	}

	result := make([]int, 0, 4)
	for _, value := range strings.Split(shardString, ",") {
		shardLength, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid shard specification: '%s'", value)
		}
		result = append(result, int(shardLength))
	}
	return result, nil
}

func (fs *fsStorage) ConnectionInfo() storage.ConnectionInfo {
	return storage.ConnectionInfo{
		Type:   fsStorageType,
		Config: &fs.Options,
	}
}

func (fs *fsStorage) Close() error {
	return nil
}

// New creates new filesystem-backed storage in a specified directory.
func New(options *Options) (storage.Storage, error) {
	var err error

	if _, err = os.Stat(options.Path); err != nil {
		return nil, fmt.Errorf("cannot access storage path: %v", err)
	}

	r := &fsStorage{
		Options: *options,
	}

	return r, nil
}

func init() {
	storage.AddSupportedStorage(
		fsStorageType,
		func() interface{} { return &Options{} },
		func(o interface{}) (storage.Storage, error) {
			return New(o.(*Options))
		})
}