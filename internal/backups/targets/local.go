package targets

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// local is a folder on this machine (a mounted drive included).
type local struct{ root string }

func openLocal(root string) (Target, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &local{root: filepath.Clean(root)}, nil
}

func (l *local) path(name string) (string, error) {
	n, err := clean(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.root, filepath.FromSlash(n)), nil
}

func (l *local) Put(_ context.Context, name string, r io.Reader) error {
	dst, err := l.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-"+filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, r)
	if err == nil {
		// On a Pi a power cut can lose what is only in the page cache.
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

func (l *local) Open(_ context.Context, name string) (io.ReadCloser, error) {
	p, err := l.path(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotExist
	}
	return f, err
}

func (l *local) List(_ context.Context, dir string) ([]string, error) {
	d, err := cleanDir(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(l.root, filepath.FromSlash(d)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (l *local) Remove(_ context.Context, name string) error {
	p, err := l.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *local) Close() error { return nil }
