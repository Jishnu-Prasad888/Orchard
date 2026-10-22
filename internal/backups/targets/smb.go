package targets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"

	"github.com/hirochachacha/go-smb2"
)

// smbTarget is a folder in an SMB (Windows / Samba / NAS) share. SMB 3
// servers encrypt the connection; sign-in is NTLM.
type smbTarget struct {
	conn  net.Conn
	sess  *smb2.Session
	share *smb2.Share
	root  string
}

func openSMB(ctx context.Context, c Config, s Secret) (Target, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr())
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", c.addr(), err)
	}
	dialer := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{
		User: c.User, Password: s.Password, Domain: c.Domain,
	}}
	sess, err := dialer.DialContext(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SMB sign-in failed: %w", err)
	}
	share, err := sess.Mount(strings.Trim(c.Share, `\/`))
	if err != nil {
		_ = sess.Logoff()
		_ = conn.Close()
		return nil, fmt.Errorf("could not open the share %q: %w", c.Share, err)
	}
	t := &smbTarget{conn: conn, sess: sess, share: share, root: strings.Trim(path.Clean("/"+c.Path), "/")}
	if t.root != "" {
		if err := share.MkdirAll(t.smbPath(t.root), 0o700); err != nil {
			_ = t.Close()
			return nil, fmt.Errorf("could not create %s in the share: %w", c.Path, err)
		}
	}
	return t, nil
}

// smbPath is a share-relative path with the separators SMB uses.
func (t *smbTarget) smbPath(p string) string { return strings.ReplaceAll(p, "/", `\`) }

func (t *smbTarget) path(name string) (string, error) {
	n, err := clean(name)
	if err != nil {
		return "", err
	}
	return t.smbPath(strings.Trim(join(t.root, n), "/")), nil
}

func (t *smbTarget) Put(ctx context.Context, name string, r io.Reader) error {
	dst, err := t.path(name)
	if err != nil {
		return err
	}
	fs := t.share.WithContext(ctx)
	dir := dst[:max(strings.LastIndex(dst, `\`), 0)]
	if dir != "" {
		if err := fs.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := strings.ReplaceAll(tempName(strings.ReplaceAll(dst, `\`, "/")), "/", `\`)
	f, err := fs.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		if err = fs.Rename(tmp, dst); err != nil {
			_ = fs.Remove(dst)
			err = fs.Rename(tmp, dst)
		}
	}
	if err != nil {
		_ = fs.Remove(tmp)
	}
	return err
}

func (t *smbTarget) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	p, err := t.path(name)
	if err != nil {
		return nil, err
	}
	f, err := t.share.WithContext(ctx).Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotExist
	}
	return f, err
}

func (t *smbTarget) List(ctx context.Context, dir string) ([]string, error) {
	d, err := cleanDir(dir)
	if err != nil {
		return nil, err
	}
	p := t.smbPath(strings.Trim(join(t.root, d), "/"))
	infos, err := t.share.WithContext(ctx).ReadDir(p)
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

func (t *smbTarget) Remove(ctx context.Context, name string) error {
	p, err := t.path(name)
	if err != nil {
		return err
	}
	if err := t.share.WithContext(ctx).Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (t *smbTarget) Close() error {
	_ = t.share.Umount()
	_ = t.sess.Logoff()
	return t.conn.Close()
}
