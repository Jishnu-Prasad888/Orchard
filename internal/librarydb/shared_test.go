package librarydb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireSharesOnePoolAndOutlivesRelease(t *testing.T) {
	dir := t.TempDir()
	a, releaseA, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, releaseB, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("second Acquire opened a new pool")
	}
	releaseA()
	releaseA() // idempotent
	if err := b.Ping(); err != nil {
		t.Fatalf("pool closed while still held: %v", err)
	}
	releaseB()
	if err := b.Ping(); err != nil {
		t.Fatalf("idle cached pool closed: %v", err)
	}
}

func TestAcquireReopensASwappedDatabase(t *testing.T) {
	dir := t.TempDir()
	first, release, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// A restore or remount puts a different file at the same path.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(dir, dbFileName+suffix))
	}
	second, release, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if first == second {
		t.Fatal("kept a pool on the replaced file")
	}
	if err := first.Ping(); err == nil {
		t.Fatal("retired idle pool was not closed")
	}
}

func TestAcquireClosesLeastRecentlyUsedPastTheCap(t *testing.T) {
	held, releaseHeld, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHeld()
	oldest, release, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release()
	for range maxShared + 1 {
		_, r, err := Acquire(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		r()
	}
	if err := oldest.Ping(); err == nil {
		t.Fatal("least recently used idle pool stayed open past the cap")
	}
	if err := held.Ping(); err != nil {
		t.Fatalf("evicted a pool still in use: %v", err)
	}
}
