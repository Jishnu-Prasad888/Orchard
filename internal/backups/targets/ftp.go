package targets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"path"
	"strings"
	"sync"

	"github.com/jlaffaye/ftp"
)

// ftpTarget is an FTP or FTPS server. FTP runs one command at a time on a
// connection, so every call holds mu — including for as long as a download
// is open.
type ftpTarget struct {
	mu   sync.Mutex
	c    *ftp.ServerConn
	root string
}

func openFTP(ctx context.Context, c Config, s Secret) (Target, error) {
	opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(dialTimeout)}
	if c.FTPSecurity != FTPPlain {
		tc := ftpsConfig(c)
		if c.FTPSecurity == FTPImplicitTLS {
			opts = append(opts, ftp.DialWithTLS(tc))
		} else {
			opts = append(opts, ftp.DialWithExplicitTLS(tc))
		}
	}
	conn, err := ftp.Dial(c.addr(), opts...)
	if err != nil {
		var unknown *UnknownServerError
		if errors.As(err, &unknown) {
			return nil, unknown
		}
		return nil, fmt.Errorf("could not connect to %s: %w", c.addr(), err)
	}
	if err := conn.Login(c.User, s.Password); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("FTP sign-in failed: %w", err)
	}
	root := path.Clean("/" + c.Path)
	if !strings.HasPrefix(c.Path, "/") {
		// A relative path is relative to the folder the account signs in to.
		if wd, err := conn.CurrentDir(); err == nil {
			root = path.Join(wd, c.Path)
		}
	}
	t := &ftpTarget{c: conn, root: root}
	if err := t.mkdirAll(t.root); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("could not create %s on the server: %w", t.root, err)
	}
	return t, nil
}

// ftpsConfig checks the server's certificate the usual way; a certificate
// that does not check out (a NAS's self-signed one) is accepted only if it is
// the one a person confirmed.
func ftpsConfig(c Config) *tls.Config {
	host := strings.TrimSpace(c.Host)
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// TLS 1.2 at most: vsftpd — what many NAS boxes run — intermittently
		// drops TLS 1.3 data connections mid-upload ("426 Failure reading
		// network stream"). TLS 1.2 with these suites is still sound.
		MaxVersion:         tls.VersionTLS12,
		ClientSessionCache: tls.NewLRUClientSessionCache(8), // data connections reuse the session
		InsecureSkipVerify: true,                            //nolint:gosec // verified in VerifyConnection below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the server sent no certificate")
			}
			leaf := cs.PeerCertificates[0]
			inter := x509.NewCertPool()
			for _, ic := range cs.PeerCertificates[1:] {
				inter.AddCert(ic)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err == nil {
				return nil
			}
			got := certFingerprint(leaf.Raw)
			if c.Fingerprint == "" {
				return &UnknownServerError{Fingerprint: got}
			}
			if got != c.Fingerprint {
				return &UnknownServerError{Fingerprint: got, Changed: true}
			}
			return nil
		},
	}
}

func (t *ftpTarget) path(name string) (string, error) {
	n, err := clean(name)
	if err != nil {
		return "", err
	}
	return join(t.root, n), nil
}

// mkdirAll makes each folder down to dir; FTP has no "make parents".
func (t *ftpTarget) mkdirAll(dir string) error {
	cur := ""
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur += "/" + seg
		if err := t.c.MakeDir(cur); err != nil && !isFTPExists(err) {
			// It may exist anyway (servers word this differently); a real
			// failure shows up when something is written there.
			continue
		}
	}
	return nil
}

func isFTPExists(err error) bool {
	var te *textproto.Error
	return errors.As(err, &te) && te.Code == ftp.StatusFileUnavailable
}

func (t *ftpTarget) Put(_ context.Context, name string, r io.Reader) error {
	dst, err := t.path(name)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.mkdirAll(path.Dir(dst)); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(dst), path.Base(tempName(path.Base(dst))))
	if err := t.c.Stor(tmp, r); err != nil {
		_ = t.c.Delete(tmp)
		return fmt.Errorf("could not upload to %s on the FTP server: %w", path.Dir(dst), err)
	}
	if err := t.c.Rename(tmp, dst); err != nil {
		// Some servers refuse to rename over an existing file.
		_ = t.c.Delete(dst)
		if err := t.c.Rename(tmp, dst); err != nil {
			_ = t.c.Delete(tmp)
			return err
		}
	}
	return nil
}

// ftpReader releases the connection when the download is closed.
type ftpReader struct {
	*ftp.Response
	unlock func()
	once   sync.Once
}

func (r *ftpReader) Close() error {
	err := r.Response.Close()
	r.once.Do(r.unlock)
	return err
}

func (t *ftpTarget) Open(_ context.Context, name string) (io.ReadCloser, error) {
	p, err := t.path(name)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	resp, err := t.c.Retr(p)
	if err != nil {
		t.mu.Unlock()
		var te *textproto.Error
		if errors.As(err, &te) && te.Code == ftp.StatusFileUnavailable {
			return nil, ErrNotExist
		}
		return nil, err
	}
	return &ftpReader{Response: resp, unlock: t.mu.Unlock}, nil
}

func (t *ftpTarget) List(_ context.Context, dir string) ([]string, error) {
	d, err := cleanDir(dir)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entries, err := t.c.List(join(t.root, d))
	if err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code == ftp.StatusFileUnavailable {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type == ftp.EntryTypeFile {
			out = append(out, path.Base(e.Name))
		}
	}
	return out, nil
}

func (t *ftpTarget) Remove(_ context.Context, name string) error {
	p, err := t.path(name)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.c.Delete(p); err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code == ftp.StatusFileUnavailable {
			return nil
		}
		return err
	}
	return nil
}

func (t *ftpTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.c.Quit()
}
