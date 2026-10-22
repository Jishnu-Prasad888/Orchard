package targets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path"
	"strings"
)

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// cleanDir is clean for a folder, where "" means the root.
func cleanDir(dir string) (string, error) {
	if strings.Trim(dir, `/\`) == "" {
		return "", nil
	}
	return clean(dir)
}

// join puts a cleaned name under the target's root.
func join(root, name string) string {
	if name == "" {
		return root
	}
	return path.Join(root, name)
}

// certFingerprint is how a pinned TLS certificate is written.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
