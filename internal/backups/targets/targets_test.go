package targets

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// contract is what every target must do; each backend runs it.
func contract(t *testing.T, tg Target) {
	t.Helper()
	ctx := context.Background()
	put := func(name, body string) {
		t.Helper()
		if err := tg.Put(ctx, name, strings.NewReader(body)); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}
	read := func(name string) string {
		t.Helper()
		rc, err := tg.Open(ctx, name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	put("objects/ab/abcdef", "first")
	if got := read("objects/ab/abcdef"); got != "first" {
		t.Errorf("read back %q", got)
	}
	// Replacing a file is allowed and atomic.
	put("objects/ab/abcdef", "second, longer than the first")
	if got := read("objects/ab/abcdef"); got != "second, longer than the first" {
		t.Errorf("after replace: %q", got)
	}
	put("objects/ab/abc999", "x")
	put("snapshots/s1.json", "{}")

	names, err := tg.List(ctx, "objects/ab")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	// No temporary files are left behind, and folders are not files.
	if strings.Join(names, ",") != "abc999,abcdef" {
		t.Errorf("list = %v", names)
	}
	if names, err := tg.List(ctx, "objects"); err != nil || len(names) != 0 {
		t.Errorf("list of a folder holding only folders = %v %v", names, err)
	}
	if names, err := tg.List(ctx, "no/such/dir"); err != nil || len(names) != 0 {
		t.Errorf("list of a missing folder = %v %v, want empty", names, err)
	}

	if _, err := tg.Open(ctx, "objects/zz/missing"); !errors.Is(err, ErrNotExist) {
		t.Errorf("open missing = %v, want ErrNotExist", err)
	}
	if err := tg.Remove(ctx, "objects/ab/abc999"); err != nil {
		t.Fatal(err)
	}
	if err := tg.Remove(ctx, "objects/ab/abc999"); err != nil {
		t.Errorf("removing a missing file = %v, want nil", err)
	}
	if names, _ := tg.List(ctx, "objects/ab"); len(names) != 1 {
		t.Errorf("after remove: %v", names)
	}

	for _, bad := range []string{"../escape", "a/../../escape", "", "/"} {
		if err := tg.Put(ctx, bad, strings.NewReader("x")); err == nil {
			t.Errorf("put %q was accepted", bad)
		}
	}

	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	if err := tg.Put(ctx, "big/file", bytes.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	if got := read("big/file"); got != string(big) {
		t.Error("1 MiB file did not round-trip")
	}
}

func TestLocalTarget(t *testing.T) {
	tg, err := Open(context.Background(), Config{Kind: KindLocal, Path: t.TempDir()}, Secret{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tg.Close() }()
	contract(t, tg)
}

// sftpServer runs an SSH server on localhost with an in-memory SFTP
// filesystem, signing in "backup" / "pw". It returns its address and host
// key fingerprint.
func sftpServer(t *testing.T) (string, string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == "backup" && string(pw) == "pw" {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	handler := sftp.InMemHandler()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					if nch.ChannelType() != "session" {
						_ = nch.Reject(ssh.UnknownChannelType, "")
						continue
					}
					ch, chReqs, err := nch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range chReqs {
							ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
							_ = req.Reply(ok, nil)
							if ok {
								srv := sftp.NewRequestServer(ch, handler)
								_ = srv.Serve()
								_ = ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()
	return ln.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey())
}

func sftpConfig(t *testing.T, addr, fingerprint string) Config {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return Config{Kind: KindSFTP, Host: host, Port: p, User: "backup", Path: "/backups/orchard", Fingerprint: fingerprint}
}

func TestSFTPTarget(t *testing.T) {
	addr, fp := sftpServer(t)
	ctx := context.Background()

	// First contact: refused, with the fingerprint to confirm.
	_, err := Open(ctx, sftpConfig(t, addr, ""), Secret{Password: "pw"})
	var unknown *UnknownServerError
	if !errors.As(err, &unknown) || unknown.Fingerprint != fp || unknown.Changed {
		t.Fatalf("first connection = %v, want an unconfirmed-server error with %s", err, fp)
	}
	// A different pinned key: refused as changed.
	_, err = Open(ctx, sftpConfig(t, addr, "SHA256:somethingelse"), Secret{Password: "pw"})
	if !errors.As(err, &unknown) || !unknown.Changed {
		t.Fatalf("changed host key = %v, want refused as changed", err)
	}
	// Wrong password.
	if _, err := Open(ctx, sftpConfig(t, addr, fp), Secret{Password: "nope"}); err == nil {
		t.Fatal("signed in with the wrong password")
	}

	tg, err := Open(ctx, sftpConfig(t, addr, fp), Secret{Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tg.Close() }()
	contract(t, tg)
}

func TestConfig_ValidateAndString(t *testing.T) {
	ok := []Config{
		{Kind: KindLocal, Path: "/media/pi/backup"},
		{Kind: KindSFTP, Host: "nas", User: "pi", Path: "backups"},
		{Kind: KindFTP, Host: "nas", User: "pi", FTPSecurity: FTPExplicitTLS},
		{Kind: KindSMB, Host: "nas", Share: "backups"},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []Config{
		{Kind: KindLocal, Path: "relative"},
		{Kind: KindSFTP, User: "pi"},
		{Kind: KindFTP, Host: "nas", User: "pi", FTPSecurity: "maybe"},
		{Kind: KindSMB, Host: "nas"},
		{Kind: "s3"},
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("%+v validated", c)
		}
	}
	if s := (Config{Kind: KindSFTP, Host: "nas", User: "pi", Path: "/b"}).String(); s != "sftp://pi@nas/b" {
		t.Errorf("sftp string = %q", s)
	}
	if s := (Config{Kind: KindSMB, Host: "nas", Share: "backups", Path: "orchard/pi"}).String(); s != `\\nas\backups\orchard\pi` {
		t.Errorf("smb string = %q", s)
	}
	if s := (Config{Kind: KindFTP, Host: "nas", Port: 2121, User: "pi", FTPSecurity: FTPPlain}).String(); s != "ftp://pi@nas:2121" {
		t.Errorf("ftp string = %q", s)
	}
}

// Real FTP and SMB servers, when the environment names them (they need a
// server to talk to, so they are not part of the default run):
//
//	ORCHARD_TEST_FTP=host:port:user:password[:explicit|implicit|none[:fingerprint]]
//	ORCHARD_TEST_SMB=host:port:share:user:password
func TestFTPTargetAgainstARealServer(t *testing.T) {
	spec := os.Getenv("ORCHARD_TEST_FTP")
	if spec == "" {
		t.Skip("ORCHARD_TEST_FTP not set")
	}
	f := strings.SplitN(spec, ":", 6)
	port, _ := strconv.Atoi(f[1])
	cfg := Config{Kind: KindFTP, Host: f[0], Port: port, User: f[2], Path: "orchard-test", FTPSecurity: FTPPlain}
	if len(f) > 4 {
		cfg.FTPSecurity = f[4]
	}
	if len(f) > 5 {
		cfg.Fingerprint = f[5]
	}
	tg, err := Open(context.Background(), cfg, Secret{Password: f[3]})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tg.Close() }()
	contract(t, tg)
}

func TestSMBTargetAgainstARealServer(t *testing.T) {
	spec := os.Getenv("ORCHARD_TEST_SMB")
	if spec == "" {
		t.Skip("ORCHARD_TEST_SMB not set")
	}
	f := strings.SplitN(spec, ":", 5)
	port, _ := strconv.Atoi(f[1])
	cfg := Config{Kind: KindSMB, Host: f[0], Port: port, Share: f[2], User: f[3], Path: "orchard-test"}
	tg, err := Open(context.Background(), cfg, Secret{Password: f[4]})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tg.Close() }()
	contract(t, tg)
}
