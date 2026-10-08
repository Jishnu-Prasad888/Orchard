package backups

import "time"

// Manifest describes the complete contents of one backup. It is stored as
// manifest.json in the backup directory and is itself written through the
// payload codec (so it is encrypted when the backup is encrypted).
type Manifest struct {
	Format    string          `json:"format"`
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	Encrypted bool            `json:"encrypted"`
	ServerDB  *Entry          `json:"server_db"`
	Libraries []LibraryBackup `json:"libraries"`
}

// LibraryBackup describes one library's metadata and media in the backup.
type LibraryBackup struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Root     string  `json:"root"`
	DB       *Entry  `json:"db"`
	Identity *Entry  `json:"identity"`
	Files    []Entry `json:"files"`
}

// Entry describes one stored payload.
type Entry struct {
	Logical    string    `json:"logical"`  // path inside the backup (slash-separated)
	Original   string    `json:"original"` // absolute source path at backup time
	Size       int64     `json:"size"`
	MTime      time.Time `json:"mtime"`
	SHA256     string    `json:"sha256"`
	Compressed bool      `json:"compressed"`
	Encrypted  bool      `json:"encrypted"`
}

// headerMetadata is the always-plaintext backup-level header (header.json).
type headerMetadata struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	Encrypted bool   `json:"encrypted"`
	Salt      string `json:"salt,omitempty"` // hex; only when encrypted
}

// manifestFilename is the manifest's file name inside a backup directory.
const manifestFilename = "manifest.json"
