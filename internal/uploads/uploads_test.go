package uploads

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	sess, err := Open(root).Create("lib", "user", "a/b.txt", 6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root).Append(sess, 0, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}

	// A fresh store (a restarted server) sees the same progress.
	again, err := Open(root).Get(sess.ID)
	if err != nil || again.Offset != 3 || again.Path != "a/b.txt" {
		t.Fatalf("reopened = %+v, %v", again, err)
	}

	var mismatch *OffsetMismatchError
	if _, err := Open(root).Append(again, 1, strings.NewReader("x")); !errors.As(err, &mismatch) || mismatch.Current != 3 {
		t.Fatalf("stale offset err = %v", err)
	}
	if _, err := Open(root).Append(again, 3, strings.NewReader("defg")); !errors.Is(err, ErrTooMuchData) {
		t.Fatalf("overflow err = %v", err)
	}
	raw, _ := os.ReadFile(Open(root).DataPath(sess.ID))
	if string(raw) != "abcdef" {
		t.Fatalf("data = %q", raw)
	}
}

func TestSweepDropsOnlyStaleSessions(t *testing.T) {
	root := t.TempDir()
	st := Open(root)
	fresh, _ := st.Create("lib", "u", "fresh.txt", 1)
	stale, _ := st.Create("lib", "u", "stale.txt", 1)
	done, _ := st.Create("lib", "u", "done.txt", 1)
	if err := st.MarkCommitted(done, "file1"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-StaleAfter - time.Hour)
	_ = os.Chtimes(st.DataPath(stale.ID), old, old)
	_ = os.Chtimes(filepath.Join(Dir(root), done.ID, sessionFile), old, old)

	st.Sweep(time.Now())

	if _, err := st.Get(fresh.ID); err != nil {
		t.Fatalf("fresh session swept: %v", err)
	}
	for _, id := range []string{stale.ID, done.ID} {
		if _, err := st.Get(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("session %s survived sweep: %v", id, err)
		}
	}
}

func TestGetRejectsPathLikeIDs(t *testing.T) {
	if _, err := Open(t.TempDir()).Get("../../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
