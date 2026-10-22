package targets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpTarget struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	root string
}

// openSFTP connects and signs in. The host key must match the pinned
// fingerprint; with none pinned, the connection is refused with the key's
// fingerprint so a person can confirm it (trust on first use, never silent).
func openSFTP(ctx context.Context, c Config, s Secret) (Target, error) {
	var auth []ssh.AuthMethod
	if s.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if s.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(s.PrivateKey), []byte(s.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(s.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("the private key could not be read: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if s.Password != "" {
		auth = append(auth, ssh.Password(s.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("a password or a private key is required")
	}
	cfg := &ssh.ClientConfig{
		User: c.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if c.Fingerprint == "" {
				return &UnknownServerError{Fingerprint: got}
			}
			if got != c.Fingerprint {
				return &UnknownServerError{Fingerprint: got, Changed: true}
			}
			return nil
		},
		Timeout: dialTimeout,
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr())
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", c.addr(), err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, c.addr(), cfg)
	if err != nil {
		_ = conn.Close()
		var unknown *UnknownServerError
		if errors.As(err, &unknown) {
			return nil, unknown
		}
		return nil, fmt.Errorf("SSH sign-in failed: %w", err)
	}
	client := ssh.NewClient(sc, chans, reqs)
	fs, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true))
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("the server does not offer SFTP: %w", err)
	}
	root := path.Clean("/" + c.Path)
	if c.Path != "" && !path.IsAbs(c.Path) {
		// A relative path is relative to the user's home folder.
		if wd, err := fs.Getwd(); err == nil {
			root = path.Join(wd, c.Path)
		}
	}
	if err := fs.MkdirAll(root); err != nil {
		_ = fs.Close()
		_ = client.Close()
		return nil, fmt.Errorf("could not create %s on the server: %w", root, err)
	}
	return &sftpTarget{ssh: client, sftp: fs, root: root}, nil
}

func (t *sftpTarget) path(name string) (string, error) {
	n, err := clean(name)
	if err != nil {
		return "", err
	}
	return join(t.root, n), nil
}

func (t *sftpTarget) Put(_ context.Context, name string, r io.Reader) error {
	dst, err := t.path(name)
	if err != nil {
		return err
	}
	if err := t.sftp.MkdirAll(path.Dir(dst)); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(dst), path.Base(tempName(path.Base(dst))))
	f, err := t.sftp.Create(tmp)
	if err != nil {
		return err
	}
	_, err = f.ReadFrom(r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// PosixRename replaces an existing file atomically where the server
		// supports it; plain Rename fails if dst exists.
		if err = t.sftp.PosixRename(tmp, dst); err != nil {
			_ = t.sftp.Remove(dst)
			err = t.sftp.Rename(tmp, dst)
		}
	}
	if err != nil {
		_ = t.sftp.Remove(tmp)
	}
	return err
}

func (t *sftpTarget) Open(_ context.Context, name string) (io.ReadCloser, error) {
	p, err := t.path(name)
	if err != nil {
		return nil, err
	}
	f, err := t.sftp.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotExist
	}
	return f, err
}

func (t *sftpTarget) List(_ context.Context, dir string) ([]string, error) {
	d, err := cleanDir(dir)
	if err != nil {
		return nil, err
	}
	infos, err := t.sftp.ReadDir(join(t.root, d))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, fi := range infos {
		if fi.Mode().IsRegular() {
			out = append(out, fi.Name())
		}
	}
	return out, nil
}

func (t *sftpTarget) Remove(_ context.Context, name string) error {
	p, err := t.path(name)
	if err != nil {
		return err
	}
	if err := t.sftp.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (t *sftpTarget) Close() error {
	err := t.sftp.Close()
	if cerr := t.ssh.Close(); err == nil {
		err = cerr
	}
	return err
}
