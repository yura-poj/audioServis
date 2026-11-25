package media

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

const MaxSize = 64 * 1024 * 1024

type Files struct {
	dir string
}

func Open(dataDir string) (*Files, error) {
	dir := filepath.Join(dataDir, "audio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Files{dir: dir}, nil
}

func (f *Files) Stage(body io.Reader) (string, int64, error) {
	file, err := os.CreateTemp(f.dir, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	size, copyErr := io.Copy(file, body)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		os.Remove(file.Name())
		return "", 0, err
	}
	return file.Name(), size, nil
}

func (f *Files) Publish(tempPath, id string) error {
	return os.Rename(tempPath, f.path(id))
}

func (f *Files) Read(id string) (*os.File, error) {
	return os.Open(f.path(id))
}

func (f *Files) Delete(id string) error {
	return os.Remove(f.path(id))
}

func (f *Files) path(id string) string {
	return filepath.Join(f.dir, id+".mp3")
}
