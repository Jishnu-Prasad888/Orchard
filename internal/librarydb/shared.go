package librarydb

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Opening a library database costs a connection, a WAL setup and a full pass
// over the schema migrations — around a millisecond on a desktop and far more
// on a Raspberry Pi's SD card — against microseconds for a query on an open
// pool. Request handlers and the indexer therefore share one open pool per
// library through Acquire, instead of opening and closing one per request.

const (
	// maxShared bounds how many library databases stay open; the least
	// recently used idle one is closed past this.
	maxShared = 16
	// sharedConns lets a few requests read in parallel (WAL allows it);
	// writers still queue on SQLite's lock via busy_timeout.
	sharedConns = 4
)

type sharedEntry struct {
	db       *sql.DB
	file     os.FileInfo // identity of library.db when opened
	refs     int
	lastUsed time.Time
	retired  bool
}

var shared = struct {
	sync.Mutex
	m map[string]*sharedEntry
}{m: map[string]*sharedEntry{}}

// Acquire returns the shared pool for the library database in orchardDir,
// opening and migrating it on first use. Call release when done; it never
// closes a pool someone else is using.
//
// Before handing out a cached pool it checks that library.db is still the
// same file: a drive that was unplugged and remounted, or a database swapped
// in by a restore, gets a fresh pool rather than a handle to the old file.
func Acquire(orchardDir string) (db *sql.DB, release func(), err error) {
	key := filepath.Clean(orchardDir)
	path := filepath.Join(key, dbFileName)

	shared.Lock()
	e := shared.m[key]
	if e != nil {
		if fi, statErr := os.Stat(path); statErr != nil || !os.SameFile(fi, e.file) {
			retireLocked(key, e)
			e = nil
		}
	}
	if e == nil {
		// Open outside the lock: migrations can take a while on a Pi and
		// must not stall every other library's requests.
		shared.Unlock()
		pool, openErr := Open(key)
		if openErr != nil {
			return nil, nil, openErr
		}
		pool.SetMaxOpenConns(sharedConns)
		pool.SetMaxIdleConns(sharedConns)
		fi, statErr := os.Stat(path)
		if statErr != nil {
			_ = pool.Close()
			return nil, nil, statErr
		}
		shared.Lock()
		if other := shared.m[key]; other != nil && os.SameFile(other.file, fi) {
			// Another request opened it meanwhile; use theirs.
			_ = pool.Close()
			e = other
		} else {
			if other != nil {
				retireLocked(key, other)
			}
			e = &sharedEntry{db: pool, file: fi}
			shared.m[key] = e
		}
	}
	e.refs++
	e.lastUsed = time.Now()
	evictLocked()
	shared.Unlock()

	return e.db, sync.OnceFunc(func() {
		shared.Lock()
		defer shared.Unlock()
		e.refs--
		e.lastUsed = time.Now()
		if e.retired && e.refs == 0 {
			_ = e.db.Close()
		}
	}), nil
}

// retireLocked drops an entry from the cache and closes it once unused.
func retireLocked(key string, e *sharedEntry) {
	if shared.m[key] == e {
		delete(shared.m, key)
	}
	e.retired = true
	if e.refs == 0 {
		_ = e.db.Close()
	}
}

// evictLocked closes least-recently-used idle pools beyond maxShared.
func evictLocked() {
	for len(shared.m) > maxShared {
		var oldestKey string
		var oldest *sharedEntry
		for k, e := range shared.m {
			if e.refs == 0 && (oldest == nil || e.lastUsed.Before(oldest.lastUsed)) {
				oldestKey, oldest = k, e
			}
		}
		if oldest == nil {
			return // every pool is in use; let the cache run over briefly
		}
		retireLocked(oldestKey, oldest)
	}
}

// Forget closes the shared pool for orchardDir (once nobody is using it), for
// when a library is removed and its database must not stay open.
func Forget(orchardDir string) {
	key := filepath.Clean(orchardDir)
	shared.Lock()
	defer shared.Unlock()
	if e := shared.m[key]; e != nil {
		retireLocked(key, e)
	}
}
