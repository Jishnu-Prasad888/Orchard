package version

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	t.Setenv("CAIRN_TEST", "1")
	info := Get()

	if strings.TrimSpace(info.Version) == "" {
		t.Errorf("version is empty")
	}
	if strings.TrimSpace(info.Commit) == "" {
		t.Errorf("commit is empty")
	}
	if strings.TrimSpace(info.BuildDate) == "" {
		t.Errorf("build date is empty")
	}
	if info.GoVersion == "" {
		t.Errorf("go version is empty")
	}
	if !strings.Contains(info.Platform, "/") {
		t.Errorf("platform %q does not contain os/arch separator", info.Platform)
	}
	if len(info.Version) > 0 && info.Version[0] == '\n' {
		t.Errorf("version has leading newline: %q", info.Version)
	}
}
