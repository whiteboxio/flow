package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

type CacheFile struct {
	path string
	ttl  time.Duration
}

func New(path string, ttl time.Duration) (*CacheFile, error) {
	return &CacheFile{
		path: path,
		ttl:  ttl,
	}, nil
}

func (f *CacheFile) Read() ([]byte, error) {
	valid, thisIsWhy := f.IsValid()
	if !valid {
		return nil, thisIsWhy
	}

	data, err := ioutil.ReadFile(f.path)
	if err != nil {
		log.Debugf("Failed to read from %s: %s", f.path, err)
	}

	return data, nil
}

func (f *CacheFile) LastModified() time.Time {
	//TODO
	return time.Now()
}

func (f *CacheFile) Consolidate(data []byte) error {
	//TODO
	return nil
}

func (f *CacheFile) IsValid() (bool, error) {
	stat, err := os.Stat(f.path)

	if os.IsNotExist(err) {
		log.Debugf("File %s does not exist, can't read", f.path)
		return false, err
	} else if err != nil {
		log.Debugf("Failed to stat file %s: %s", f.path, err)
		return false, err
	}

	modTime := stat.ModTime()
	modSince := time.Now().Sub(modTime)
	if modSince > f.ttl {
		errMsg := fmt.Sprintf("File %s has expired (TTL: %f, modified: %f seconds ago)",
			f.path, f.ttl.Seconds(), modSince.Seconds())
		log.Debugf(errMsg)
		return false, fmt.Errorf(errMsg)
	}
	return true, nil
}

func (f *CacheFile) Invalidate() error {
	//TODO
	return nil
}