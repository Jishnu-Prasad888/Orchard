package dictionary

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/uploads"
)

// Phases of an install job.
const (
	PhaseDownloading = "downloading"
	PhaseVerifying   = "verifying"
	PhaseExtracting  = "extracting"
	PhaseIndexing    = "indexing"
	PhaseDone        = "done"
	PhaseFailed      = "failed"
	PhaseCancelled   = "cancelled"
)

// stallTimeout fails a download that has received nothing for this long, in
// place of an overall timeout that would cut off a slow but working link.
const stallTimeout = 60 * time.Second

// freeSpaceMargin is kept free beyond what an install itself needs.
const freeSpaceMargin = 64 << 20

// Job is the state of an install, published for the settings page to poll.
// The counters are updated from the install goroutine and read from request
// handlers, so they are atomics; everything else is fixed at creation or
// written once under the manager's lock.
type Job struct {
	ID           string
	DictionaryID string
	Name         string
	StartedAt    time.Time

	phase    atomic.Value // string
	done     atomic.Int64
	total    atomic.Int64
	errMsg   atomic.Value // string
	finished atomic.Value // time.Time
}

// JobStatus is a point-in-time copy of a Job.
type JobStatus struct {
	ID           string `json:"id"`
	DictionaryID string `json:"dictionary_id"`
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	// Done and Total measure the current phase (bytes downloaded, bytes
	// extracted, entries indexed); Total is 0 when it can't be known.
	Done       int64      `json:"done"`
	Total      int64      `json:"total"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (j *Job) setPhase(phase string, total int64) {
	j.done.Store(0)
	j.total.Store(total)
	j.phase.Store(phase)
}

// Status snapshots the job.
func (j *Job) Status() JobStatus {
	s := JobStatus{
		ID:           j.ID,
		DictionaryID: j.DictionaryID,
		Name:         j.Name,
		Phase:        j.phase.Load().(string),
		Done:         j.done.Load(),
		Total:        j.total.Load(),
		StartedAt:    j.StartedAt,
	}
	if msg, _ := j.errMsg.Load().(string); msg != "" {
		s.Error = msg
	}
	if t, ok := j.finished.Load().(time.Time); ok && !t.IsZero() {
		s.FinishedAt = &t
	}
	return s
}

func (j *Job) running() bool {
	switch j.phase.Load().(string) {
	case PhaseDone, PhaseFailed, PhaseCancelled:
		return false
	}
	return true
}

// Job returns the current or most recent install, or nil if there has been
// none since the server started.
func (m *Manager) Job() *JobStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.job == nil {
		return nil
	}
	s := m.job.Status()
	return &s
}

// Install starts installing the dictionary with the given source id in the
// background and returns at once; follow it with Job.
func (m *Manager) Install(ctx context.Context, id string) (*JobStatus, error) {
	src, err := m.source(ctx, id)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.job != nil && m.job.running() {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	if _, ok := m.installed[id]; ok {
		m.mu.Unlock()
		return nil, ErrInstalled
	}
	job := &Job{
		ID:           newJobID(),
		DictionaryID: src.ID,
		Name:         src.Name,
		StartedAt:    time.Now().UTC(),
	}
	job.setPhase(PhaseDownloading, src.DownloadBytes)
	// The install outlives the request that started it; only Cancel (or
	// shutdown, via Close) stops it.
	runCtx, cancel := context.WithCancel(context.Background())
	m.job = job
	m.cancel = cancel
	m.mu.Unlock()

	go m.run(runCtx, job, src)
	s := job.Status()
	return &s, nil
}

// Cancel stops the running install.
func (m *Manager) Cancel() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || !m.job.running() || m.cancel == nil {
		return ErrNoJob
	}
	m.cancel()
	return nil
}

func (m *Manager) run(ctx context.Context, job *Job, src Source) {
	staging := filepath.Join(m.dir, ".install-"+job.ID)
	err := m.install(ctx, job, src, staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		switch {
		case errors.Is(err, context.Canceled):
			job.phase.Store(PhaseCancelled)
		default:
			m.logger.Error("install dictionary", "id", src.ID, "error", err)
			job.errMsg.Store(err.Error())
			job.phase.Store(PhaseFailed)
		}
	}
	job.finished.Store(time.Now().UTC())

	m.mu.Lock()
	if m.job == job {
		m.cancel = nil
	}
	m.mu.Unlock()
}

func (m *Manager) install(ctx context.Context, job *Job, src Source, staging string) error {
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return fmt.Errorf("prepare install: %w", err)
	}
	// Roughly: the download, plus what it unpacks to (both formats unpack
	// to about three times their compressed size).
	if src.DownloadBytes > 0 {
		if free, ok := freeBytes(m.dir); ok && free < src.DownloadBytes*4+freeSpaceMargin {
			return fmt.Errorf("not enough free space on the server's data disk (needs about %d MB)",
				(src.DownloadBytes*4+freeSpaceMargin)>>20)
		}
	}

	archive := filepath.Join(staging, "download")
	if err := m.download(ctx, job, src, archive); err != nil {
		return err
	}

	var err error
	switch src.Kind {
	case KindWordNet:
		err = installWordNet(ctx, job, archive, staging)
	case KindFreeDict:
		err = installFreeDict(ctx, job, archive, staging)
	default:
		err = fmt.Errorf("unknown dictionary kind %q", src.Kind)
	}
	if err != nil {
		return err
	}
	_ = os.Remove(archive)

	info := Installed{
		ID:          src.ID,
		Kind:        src.Kind,
		Name:        src.Name,
		Description: src.Description,
		Version:     src.Version,
		License:     src.License,
		InstalledAt: time.Now().UTC(),
		Enabled:     true,
	}
	info.SizeBytes = dirSize(staging)
	if err := writeManifest(staging, info); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	final := filepath.Join(m.dir, src.ID)
	_ = os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		return fmt.Errorf("move dictionary into place: %w", err)
	}
	m.installed[src.ID] = &installedDict{info: info}
	job.phase.Store(PhaseDone)
	return nil
}

// download streams src to path, publishing progress on job and verifying
// the checksum as the bytes go by.
func (m *Manager) download(ctx context.Context, job *Job, src Source, path string) error {
	// A watchdog cancels the request when no data has arrived for
	// stallTimeout; every read that brings data pushes it back.
	reqCtx, cancelReq := context.WithCancelCause(ctx)
	defer cancelReq(nil)
	errStalled := errors.New("the download stalled (no data for a minute); try again")
	watchdog := time.AfterFunc(stallTimeout, func() { cancelReq(errStalled) })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, src.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Orchard (self-hosted library; dictionary download)")
	resp, err := m.client.Do(req)
	if err != nil {
		return downloadError(ctx, reqCtx, fmt.Errorf("couldn't reach the download server: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the download server answered %s", resp.Status)
	}
	if resp.ContentLength > 0 {
		job.total.Store(resp.ContentLength)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	defer func() { _ = f.Close() }()

	algo, want, _ := strings.Cut(src.Checksum, ":")
	var sum hash.Hash
	switch algo {
	case "sha256":
		sum = sha256.New()
	case "sha512":
		sum = sha512.New()
	}
	var w io.Writer = f
	if sum != nil {
		w = io.MultiWriter(f, sum)
	}

	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallTimeout)
			if _, err := w.Write(buf[:n]); err != nil {
				return fmt.Errorf("save download: %w", err)
			}
			job.done.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return downloadError(ctx, reqCtx, fmt.Errorf("the download was interrupted: %w", rerr))
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("save download: %w", err)
	}

	if sum != nil {
		job.setPhase(PhaseVerifying, 0)
		if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want) {
			return errors.New("the download is damaged (its checksum doesn't match); try again")
		}
	}
	return nil
}

// downloadError explains a failed request: a cancelled install, a stall
// the watchdog caught, or the network error itself.
func downloadError(ctx, reqCtx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cause := context.Cause(reqCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 20 * time.Second}).DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
}

func newJobID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// freeBytes reports the space available on the filesystem holding path.
func freeBytes(path string) (int64, bool) { return uploads.FreeBytes(path) }
