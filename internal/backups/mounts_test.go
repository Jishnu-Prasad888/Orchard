package backups

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
)

// A Raspberry Pi with the SD card at / and a USB drive at /media/pi/backup.
const piMountinfo = `22 1 179:2 / / rw,noatime shared:1 - ext4 /dev/mmcblk0p2 rw
23 22 0:5 / /dev rw - devtmpfs udev rw
24 22 0:21 / /proc rw - proc proc rw
25 22 179:1 / /boot/firmware rw - vfat /dev/mmcblk0p1 rw
40 22 8:1 / /media/pi/backup rw,nosuid - exfat /dev/sda1 rw
41 22 0:50 / /media/pi/My\040Photos rw - ext4 /dev/sdb1 rw
`

func piMounts() []Mount {
	mounts := parseMountinfo(strings.NewReader(piMountinfo))
	for i := range mounts {
		mounts[i].System = mounts[i].Path == "/"
		if mounts[i].Device == "/dev/sda1" {
			mounts[i].UUID = "AAAA-1111"
		}
	}
	return mounts
}

func TestParseMountinfo(t *testing.T) {
	mounts := parseMountinfo(strings.NewReader(piMountinfo))
	if len(mounts) != 6 {
		t.Fatalf("parsed %d mounts, want 6", len(mounts))
	}
	if m := mounts[5]; m.Path != "/media/pi/My Photos" || m.FSType != "ext4" || m.Device != "/dev/sdb1" {
		t.Errorf("escaped mount point = %+v", m)
	}
	m, _ := mountFor(mounts, "/media/pi/backup/orchard")
	if m.Path != "/media/pi/backup" {
		t.Errorf("mount for a folder on the drive = %q", m.Path)
	}
	if m, _ := mountFor(mounts, "/media/pi/backupX/orchard"); m.Path != "/" {
		t.Errorf("a sibling with a longer name is not on the drive, got %q", m.Path)
	}
}

func TestCheckDrive(t *testing.T) {
	plugged := piMounts()
	rec := recordDrive(plugged, "/media/pi/backup/orchard")
	if rec.Mount != "/media/pi/backup" || rec.UUID != "AAAA-1111" {
		t.Fatalf("recorded drive = %+v", rec)
	}
	if msg := checkDrive(plugged, rec, "/media/pi/backup/orchard"); msg != "" {
		t.Errorf("drive plugged in, but: %s", msg)
	}

	// Unplugged: /media/pi/backup is now just a folder on the SD card.
	var unplugged []Mount
	for _, m := range plugged {
		if m.Path != "/media/pi/backup" {
			unplugged = append(unplugged, m)
		}
	}
	if msg := checkDrive(unplugged, rec, "/media/pi/backup/orchard"); !strings.Contains(msg, "not connected") {
		t.Errorf("unplugged drive: %q", msg)
	}

	// Another drive at the same place.
	other := piMounts()
	for i := range other {
		if other[i].Path == "/media/pi/backup" {
			other[i].UUID = "BBBB-2222"
		}
	}
	if msg := checkDrive(other, rec, "/media/pi/backup/orchard"); !strings.Contains(msg, "different drive") {
		t.Errorf("different drive: %q", msg)
	}

	// Never recorded, but under /media and on the system disk: refused.
	if msg := checkDrive(unplugged, DriveRecord{}, "/media/pi/backup/orchard"); msg == "" {
		t.Error("an unrecorded folder under /media on the SD card should be refused")
	}
	// A folder on the system disk outside /media and /mnt is a choice, not
	// a missing drive.
	if msg := checkDrive(plugged, DriveRecord{}, "/srv/orchard-backups"); msg != "" {
		t.Errorf("/srv on the system disk: %q", msg)
	}
	// No mount table (another OS): nothing to check.
	if msg := checkDrive(nil, rec, "/media/pi/backup/orchard"); msg != "" {
		t.Errorf("no mount table: %q", msg)
	}
}

func TestDrivesListsRealFilesystems(t *testing.T) {
	// On the machine running the tests: at least the system disk, and never
	// proc or sysfs.
	drives := Drives()
	if len(drives) == 0 {
		t.Skip("no mount table on this platform")
	}
	if !drives[0].System {
		t.Errorf("the system disk should come first: %+v", drives[0])
	}
	for _, d := range drives {
		if virtualFS[d.FSType] {
			t.Errorf("listed a virtual filesystem: %+v", d)
		}
	}
}

func TestManager_RefusesAnUnpluggedDriveAndWritesNothing(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	ctx := context.Background()
	m := newManager(t, pool, serverPath, "", "", 4)
	if err := m.LoadPolicy(ctx, serversettings.NewStore(pool)); err != nil {
		t.Fatal(err)
	}
	// The "USB drive" is a temp folder; the fake mount table says what is
	// mounted where.
	drive := t.TempDir()
	dir := filepath.Join(drive, "orchard")
	plugged := []Mount{{Path: "/", Device: "/dev/mmcblk0p2", System: true},
		{Path: drive, Device: "/dev/sda1", UUID: "AAAA-1111"}}
	m.mounts = func() []Mount { return plugged }
	if err := m.SetDir(dir, DirFromSetting); err != nil {
		t.Fatalf("choose a folder on a plugged-in drive: %v", err)
	}
	if st := m.Drive(); !st.Connected || st.Mount != drive {
		t.Errorf("drive status = %+v", st)
	}

	// Unplugged.
	m.mounts = func() []Mount { return plugged[:1] }
	if st := m.Drive(); st.Connected || !strings.Contains(st.Problem, "not connected") {
		t.Errorf("unplugged drive status = %+v", st)
	}
	rec, err := m.RunBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusFailed || !strings.Contains(rec.ErrorMsg, "not connected") {
		t.Errorf("backup to an unplugged drive = %s %q, want failed: not connected", rec.Status, rec.ErrorMsg)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("wrote %d entries where the drive should be", len(entries))
	}

	// Plugged back in: backs up again.
	m.mounts = func() []Mount { return plugged }
	if rec, err := m.RunBackup(ctx); err != nil || rec.Status != StatusCompleted {
		t.Errorf("after plugging back in: %v %v", rec, err)
	}
}
