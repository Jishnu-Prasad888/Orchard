package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/safeimage"
)

// videoFrameTimeout bounds one ffmpeg invocation, so a damaged or enormous
// file can never hold a worker (or a thumbnail request) indefinitely.
const videoFrameTimeout = 30 * time.Second

// MaxPosterBytes caps a client-captured video frame (PUT .../thumbnail).
const MaxPosterBytes = 8 << 20

// ErrNoVideoDecoder is returned when no ffmpeg binary is on PATH.
var ErrNoVideoDecoder = errors.New("ffmpeg is not available")

// ffmpegPath is resolved once; an empty result means video frames cannot be
// extracted on the server and clients capture them instead.
var ffmpegPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
})

// VideoFramesAvailable reports whether the server can extract video frames.
func VideoFramesAvailable() bool { return ffmpegPath() != "" }

// extractVideoFrame decodes the first frame of the video at path. The video
// is only read; ffmpeg writes the frame as a PNG to its stdout.
func extractVideoFrame(ctx context.Context, path string) (image.Image, error) {
	bin := ffmpegPath()
	if bin == "" {
		return nil, ErrNoVideoDecoder
	}
	ctx, cancel := context.WithTimeout(ctx, videoFrameTimeout)
	defer cancel()

	// "-scale" keeps the decoded frame small before it reaches Go; the
	// thumbnail is resized again by resizeToFit with the photo pipeline.
	cmd := exec.CommandContext(ctx, bin,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path,
		"-map", "0:v:0", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", ThumbnailSize*2),
		"-f", "image2pipe", "-vcodec", "png", "-",
	)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	img, _, err := safeimage.Decode(bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("decode video frame: %w", err)
	}
	return img, nil
}

// StorePoster stores a video frame captured by a client (the browser draws
// the video's first frame to a canvas) as the file's thumbnail. The bytes are
// decoded with the same safety limits as photos and re-encoded, so nothing a
// client sends is written verbatim. An existing thumbnail is kept: a frame
// the server extracted itself always wins.
func StorePoster(r io.Reader, cairnDir, fileID string, keys *crypto.Keys) (stored bool, err error) {
	if _, _, err := ReadThumb(cairnDir, fileID, keys); err == nil {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxPosterBytes+1))
	if err != nil {
		return false, fmt.Errorf("read poster: %w", err)
	}
	if len(data) > MaxPosterBytes {
		return false, fmt.Errorf("poster exceeds %d bytes", MaxPosterBytes)
	}
	img, _, err := safeimage.Decode(bytes.NewReader(data))
	if err != nil {
		return false, fmt.Errorf("decode poster: %w", err)
	}
	if err := writeThumb(img, cairnDir, fileID, keys); err != nil {
		return false, err
	}
	return true, nil
}
