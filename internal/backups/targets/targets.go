// Package targets is where backups are written: a folder on this machine
// (including a mounted drive), or an SFTP, FTP(S) or SMB server.
//
// A Target is deliberately small — put, open, list, remove — so a backup
// store can be built on any of them the same way. Names are slash-separated
// and relative to the target's root folder; nothing can name a file above
// it. Put is atomic: a file is uploaded under a temporary name and renamed
// into place, so an interrupted run never leaves half a file under a real
// name.
package targets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Kinds of target.
const (
	KindLocal = "local"
	KindSFTP  = "sftp"
	KindFTP   = "ftp"
	KindSMB   = "smb"
)

// FTP security.
const (
	FTPExplicitTLS = "explicit" // FTPS: AUTH TLS on the normal port (21)
	FTPImplicitTLS = "implicit" // FTPS: TLS from the first byte (port 990)
	FTPPlain       = "none"     // plain FTP: password and files unencrypted
)

// dialTimeout bounds connecting to a server that is not answering.
const dialTimeout = 15 * time.Second

// Target is a place backups are stored.
type Target interface {
	// Put stores r under name, replacing any file there, creating folders
	// as needed. Atomic: readers see the old file or the whole new one.
	Put(ctx context.Context, name string, r io.Reader) error
	// Open reads name. A missing file is an error wrapping ErrNotExist.
	Open(ctx context.Context, name string) (io.ReadCloser, error)
	// List names the files (not folders) directly in dir; a missing dir is
	// an empty list.
	List(ctx context.Context, dir string) ([]string, error)
	// Remove deletes name; a missing file is not an error.
	Remove(ctx context.Context, name string) error
	// Close ends the connection.
	Close() error
}

// ErrNotExist is a file that is not there.
var ErrNotExist = errors.New("file does not exist")

// Config is a target's settings. It never holds a secret; Secret does.
type Config struct {
	Kind string `json:"kind"`
	// Path is the folder backups go in: an absolute path for "local", a
	// path on the server for SFTP and FTP, a folder inside Share for SMB.
	Path string `json:"path"`
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	User string `json:"user,omitempty"`
	// Share is the SMB share name ("backups" in \\nas\backups).
	Share string `json:"share,omitempty"`
	// Domain is the SMB/Windows domain, if any.
	Domain string `json:"domain,omitempty"`
	// FTPSecurity is FTPExplicitTLS, FTPImplicitTLS or FTPPlain.
	FTPSecurity string `json:"ftp_security,omitempty"`
	// Fingerprint pins the server's identity when it cannot be checked any
	// other way: the SSH host key for SFTP ("SHA256:…"), or a self-signed
	// FTPS certificate. Set when a person confirms it on first connection.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Secret is what signs in: a password, or for SFTP a private key.
type Secret struct {
	Password      string `json:"password,omitempty"`
	PrivateKey    string `json:"private_key,omitempty"`
	KeyPassphrase string `json:"key_passphrase,omitempty"`
}

// UnknownServerError is a server whose identity has not been confirmed (no
// fingerprint pinned yet) or has changed since it was. Fingerprint is what
// the server presented, for a person to check and confirm.
type UnknownServerError struct {
	Fingerprint string
	Changed     bool
}

func (e *UnknownServerError) Error() string {
	if e.Changed {
		return "the server's identity has changed since it was confirmed (now " + e.Fingerprint +
			"). This can mean someone is intercepting the connection; if the server was reinstalled, confirm the new identity."
	}
	return "confirm the server's identity: " + e.Fingerprint
}

// Validate reports settings that could not work.
func (c Config) Validate() error {
	switch c.Kind {
	case KindLocal:
		if !path.IsAbs(c.Path) && !isWindowsAbs(c.Path) {
			return errors.New("the folder must be an absolute path")
		}
		return nil
	case KindSFTP, KindFTP, KindSMB:
		if strings.TrimSpace(c.Host) == "" {
			return errors.New("the server address is required")
		}
		if c.Port < 0 || c.Port > 65535 {
			return errors.New("the port must be 1–65535")
		}
		if c.Kind != KindSMB && strings.TrimSpace(c.User) == "" {
			return errors.New("the user name is required")
		}
		if c.Kind == KindSMB && strings.Trim(c.Share, `\/ `) == "" {
			return errors.New("the share name is required")
		}
		if c.Kind == KindFTP {
			switch c.FTPSecurity {
			case FTPExplicitTLS, FTPImplicitTLS, FTPPlain:
			default:
				return errors.New(`ftp_security must be "explicit", "implicit" or "none"`)
			}
		}
		return nil
	}
	return fmt.Errorf("kind must be %q, %q, %q or %q", KindLocal, KindSFTP, KindFTP, KindSMB)
}

func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// String names the target for people: never a password.
func (c Config) String() string {
	switch c.Kind {
	case KindLocal:
		return c.Path
	case KindSMB:
		return fmt.Sprintf(`\\%s\%s%s`, c.Host, strings.Trim(c.Share, `\/`), winPath(c.Path))
	default:
		host := c.Host
		if c.Port != 0 && c.Port != c.defaultPort() {
			host = fmt.Sprintf("%s:%d", c.Host, c.Port)
		}
		return fmt.Sprintf("%s://%s@%s%s", c.Kind, c.User, host, slashed(c.Path))
	}
}

func (c Config) defaultPort() int {
	switch c.Kind {
	case KindSFTP:
		return 22
	case KindSMB:
		return 445
	case KindFTP:
		if c.FTPSecurity == FTPImplicitTLS {
			return 990
		}
		return 21
	}
	return 0
}

func (c Config) addr() string {
	port := c.Port
	if port == 0 {
		port = c.defaultPort()
	}
	return fmt.Sprintf("%s:%d", strings.TrimSpace(c.Host), port)
}

func slashed(p string) string {
	p = "/" + strings.Trim(p, "/")
	if p == "/" {
		return ""
	}
	return p
}

func winPath(p string) string {
	p = strings.Trim(strings.ReplaceAll(p, "/", `\`), `\`)
	if p == "" {
		return ""
	}
	return `\` + p
}

// Open connects to the target the config names.
func Open(ctx context.Context, c Config, s Secret) (Target, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	switch c.Kind {
	case KindLocal:
		return openLocal(c.Path)
	case KindSFTP:
		return openSFTP(ctx, c, s)
	case KindFTP:
		return openFTP(ctx, c, s)
	case KindSMB:
		return openSMB(ctx, c, s)
	}
	return nil, errors.New("unknown target kind")
}

// clean checks a name and returns it slash-separated with no leading slash.
// Nothing may climb out of the target's root.
func clean(name string) (string, error) {
	raw := strings.ReplaceAll(name, `\`, "/")
	// A ".." anywhere is refused rather than quietly folded away: a name
	// that tries to climb is a bug or an attack, never a file to write.
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid name %q", name)
		}
	}
	n := path.Clean("/" + raw)
	if n == "/" || strings.Contains(name, "\x00") {
		return "", fmt.Errorf("invalid name %q", name)
	}
	return n[1:], nil
}

// tempName is where Put writes before renaming into place.
func tempName(name string) string {
	dir, file := path.Split(name)
	return dir + ".tmp-" + file + "-" + randomSuffix()
}
