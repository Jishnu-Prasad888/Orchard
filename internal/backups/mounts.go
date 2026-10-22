package backups

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A backup folder on a USB drive is only a folder on that drive while the
// drive is plugged in. Unplugged, /media/usb is an empty directory on the
// system disk — on a Raspberry Pi, the SD card — and a backup written there
// fills the card instead. So Orchard notes which drive a backup folder lives
// on and refuses to run when a different filesystem (or none) is there.

// Mount is one mounted filesystem.
type Mount struct {
	// Path is where it is mounted.
	Path string `json:"path"`
	// Device is what is mounted ("/dev/sda1", "//nas/share").
	Device string `json:"device"`
	FSType string `json:"fs_type"`
	// UUID is the filesystem's UUID when the system knows it (block
	// devices); it tells two drives apart at the same mount point.
	UUID string `json:"uuid,omitempty"`
	// Total and Free are in bytes (0 when unknown).
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
	// System is true for the filesystem "/" is on.
	System bool `json:"system"`
}

// virtualFS are filesystems that are never a place to put a backup.
var virtualFS = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "securityfs": true, "pstore": true, "bpf": true,
	"debugfs": true, "tracefs": true, "configfs": true, "fusectl": true, "mqueue": true,
	"hugetlbfs": true, "autofs": true, "binfmt_misc": true, "rpc_pipefs": true,
	"efivarfs": true, "ramfs": true, "nsfs": true, "squashfs": true, "overlay": true,
	"fuse.portal": true, "fuse.gvfsd-fuse": true,
}

// parseMountinfo reads /proc/self/mountinfo.
func parseMountinfo(r io.Reader) []Mount {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		// id parent maj:min root mountpoint opts [optional...] - fstype source superopts
		left, right, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		lf, rf := strings.Fields(left), strings.Fields(right)
		if len(lf) < 5 || len(rf) < 2 {
			continue
		}
		out = append(out, Mount{
			Path:   unescapeMount(lf[4]),
			FSType: rf[0],
			Device: unescapeMount(rf[1]),
		})
	}
	return out
}

// unescapeMount undoes mountinfo's octal escapes (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := 0
			okOct := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					okOct = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if okOct {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountFor is the mount that contains path: the longest mount point that is
// path or an ancestor of it.
func mountFor(mounts []Mount, path string) (Mount, bool) {
	var best Mount
	found := false
	for _, m := range mounts {
		if m.Path == path || m.Path == "/" || strings.HasPrefix(path, strings.TrimSuffix(m.Path, "/")+"/") {
			if !found || len(m.Path) > len(best.Path) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// uuidsByDevice maps a device path to its filesystem UUID.
func uuidsByDevice(byUUIDDir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(byUUIDDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		target, err := filepath.EvalSymlinks(filepath.Join(byUUIDDir, e.Name()))
		if err == nil {
			out[target] = e.Name()
		}
	}
	return out
}

// systemMounts lists the mounted filesystems, with UUIDs and space filled
// in. Empty where the platform has no /proc/self/mountinfo.
func systemMounts() []Mount {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	mounts := parseMountinfo(f)
	uuids := uuidsByDevice("/dev/disk/by-uuid")
	root, _ := mountFor(mounts, "/")
	for i := range mounts {
		m := &mounts[i]
		if dev, err := filepath.EvalSymlinks(m.Device); err == nil {
			m.UUID = uuids[dev]
		}
		m.System = m.Path == root.Path
	}
	return mounts
}

// Drives is where a backup could go: real filesystems, the system disk
// first, then the rest by mount point, each with its free space.
func Drives() []Mount {
	var out []Mount
	seen := map[string]bool{}
	for _, m := range systemMounts() {
		if virtualFS[m.FSType] || strings.HasPrefix(m.Path, "/proc") ||
			strings.HasPrefix(m.Path, "/sys") || strings.HasPrefix(m.Path, "/dev") ||
			strings.HasPrefix(m.Path, "/boot") || strings.HasPrefix(m.Path, "/snap") ||
			seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		m.Total, m.Free = diskSpace(m.Path)
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return out[i].System
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// DriveRecord is which drive a backup folder was chosen on.
type DriveRecord struct {
	Dir   string `json:"dir"`
	Mount string `json:"mount"`
	UUID  string `json:"uuid,omitempty"`
}

// recordDrive notes the drive dir is on now.
func recordDrive(mounts []Mount, dir string) DriveRecord {
	m, _ := mountFor(mounts, dir)
	return DriveRecord{Dir: dir, Mount: m.Path, UUID: m.UUID}
}

// removablePrefixes are where drives are mounted by convention. A backup
// folder under one of them that is sitting on the system disk is a drive
// that is not plugged in, recorded or not.
var removablePrefixes = []string{"/media/", "/mnt/", "/run/media/"}

// checkDrive explains why dir cannot be backed up to right now, or "".
func checkDrive(mounts []Mount, rec DriveRecord, dir string) string {
	if len(mounts) == 0 {
		return "" // no mount table here: nothing to check against
	}
	cur, ok := mountFor(mounts, dir)
	if !ok {
		return ""
	}
	if rec.Dir == dir && rec.Mount != "" {
		if cur.Path != rec.Mount {
			return "The backup drive for " + dir + " (" + rec.Mount + ") is not connected. " +
				"Plug it in, or choose another backup folder."
		}
		if rec.UUID != "" && cur.UUID != "" && cur.UUID != rec.UUID {
			return "A different drive is mounted at " + rec.Mount + " than the one backups were set up on. " +
				"Plug in the backup drive, or choose this folder again to use the new one."
		}
		return ""
	}
	if cur.System {
		for _, p := range removablePrefixes {
			if strings.HasPrefix(dir+"/", p) {
				return "Nothing is mounted at " + dir + ": it is a folder on the system disk, so the " +
					"backup drive is probably not plugged in. Plug it in, or choose another backup folder."
			}
		}
	}
	return ""
}
