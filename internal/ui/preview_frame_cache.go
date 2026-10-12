package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

const (
	previewFrameMagic        = "CCFRAME1"
	previewFrameHeaderSize   = 8 + 8 + sha256.Size
	previewFrameByteLimit    = 16 * 1024 * 1024
	previewCacheByteLimit    = 64 * 1024 * 1024
	previewCacheEntryLimit   = 128
	previewSessionClosedFile = "preview-session-closed"
	previewFrameSessionEnv   = "CATCLIP_INTERNAL_PREVIEW_SESSION"
)

type previewFrameCache struct {
	dir, path          string
	artifactOwner      string
	claimPath          string
	maxFrame, maxBytes int
	maxEntries         int
}

// RunSessionPreview replays a completed frame owned by an immutable temporary
// artifact. The caller coordinates predecessor cancellation BEFORE entering.
// No source stat, discovery, or eager render is added. Empty artifacts disable
// reuse; errors in storage always fall back to the supplied normal renderer.
func RunSessionPreview(ctx context.Context, stdout io.Writer, artifact, kind string, key any, render func(io.Writer) error) error {
	cache := newPreviewFrameCache(artifact, kind, key)
	return cache.render(ctx, stdout, render)
}

func newPreviewFrameCache(artifact, kind string, key any) previewFrameCache {
	cache := previewFrameCache{maxFrame: previewFrameByteLimit, maxBytes: previewCacheByteLimit, maxEntries: previewCacheEntryLimit}
	if strings.TrimSpace(artifact) == "" {
		return cache
	}
	// Artifact paths are unique for their existing generation/state owner. Do
	// not hash/decode the inventory or serialize its entries for every lookup.
	encoded, err := json.Marshal(struct {
		Version        int
		Artifact, Kind string
		Request        any
	}{1, artifact, kind, key})
	if err != nil {
		return cache
	}
	sum := sha256.Sum256(encoded)
	cache.dir = filepath.Join(filepath.Dir(artifact), "preview-frames")
	cache.artifactOwner = filepath.Dir(artifact)
	if session := os.Getenv(previewFrameSessionEnv); session != "" {
		cache.dir = filepath.Join(session, "preview-frames")
	}
	cache.path = filepath.Join(cache.dir, hex.EncodeToString(sum[:])+".frame")
	if kind == "target-tree" {
		if session := os.Getenv(picker.TargetPreviewSessionEnv); session != "" {
			cache.claimPath = filepath.Join(session, picker.TargetPreviewPIDFile)
		}
	} else {
		cache.claimPath = predecessorPidPath(artifact, predecessorBucketFilePreview)
	}
	return cache
}

func (cache previewFrameCache) render(ctx context.Context, stdout io.Writer, render func(io.Writer) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	writer := &previewObservedWriter{ctx: ctx, w: stdout}
	if cache.path == "" || cache.sessionClosed() {
		return render(writer)
	}
	if body, ok := cache.read(); ok {
		platform.InternalBenchLog("ui.preview_frame_cache", "hit", "true", "bytes", platform.InternalBenchInt(len(body)))
		n, err := writer.Write(body)
		if err != nil {
			return err
		}
		if n != len(body) {
			return io.ErrShortWrite
		}
		now := time.Now()
		_ = os.Chtimes(cache.path, now, now)
		return nil
	}
	platform.InternalBenchLog("ui.preview_frame_cache", "hit", "false")
	// Bound the extra copy even when a full scrollable preview is enormous.
	// Oversize output continues normally; only storage is abandoned.
	capture := &previewFrameCapture{limit: cache.maxFrame}
	if err := render(io.MultiWriter(writer, capture)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Some existing streaming previews deliberately swallow a closed pipe.
	// Preserve that return behavior but never publish the partial capture.
	if writer.err != nil {
		return nil
	}
	if !capture.oversize && capture.Len() > 0 && cache.ownsPublication() {
		cache.publish(ctx, capture.Bytes())
	}
	return nil
}

type previewObservedWriter struct {
	ctx context.Context
	w   io.Writer
	err error
}

func (w *previewObservedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

// Preserve the StringWriter fast path used by tree rendering. Falling back to
// []byte for every colored fragment adds allocations throughout a large frame.
func (w *previewObservedWriter) WriteString(s string) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	n, err := io.WriteString(w.w, s)
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

type previewFrameCapture struct {
	bytes.Buffer
	limit    int
	oversize bool
}

func (capture *previewFrameCapture) Write(p []byte) (int, error) {
	if !capture.oversize {
		if len(p) > capture.limit-capture.Len() {
			capture.oversize = true
			capture.Buffer = bytes.Buffer{}
		} else {
			_, _ = capture.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// bytes.Buffer's promoted WriteString would bypass Write and its limit when
// io.MultiWriter chooses StringWriter. Both entry points must enforce the cap.
func (capture *previewFrameCapture) WriteString(s string) (int, error) {
	if !capture.oversize {
		if len(s) > capture.limit-capture.Len() {
			capture.oversize = true
			capture.Buffer = bytes.Buffer{}
		} else {
			_, _ = capture.Buffer.WriteString(s)
		}
	}
	return len(s), nil
}

func (cache previewFrameCache) read() ([]byte, bool) {
	f, err := os.Open(cache.path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(cache.maxFrame+previewFrameHeaderSize) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(cache.maxFrame+previewFrameHeaderSize+1)))
	if err != nil || len(data) < previewFrameHeaderSize || len(data) > cache.maxFrame+previewFrameHeaderSize {
		return nil, false
	}
	if string(data[:8]) != previewFrameMagic || binary.LittleEndian.Uint64(data[8:16]) != uint64(len(data)-previewFrameHeaderSize) {
		return nil, false
	}
	body := data[previewFrameHeaderSize:]
	sum := sha256.Sum256(body)
	if !bytes.Equal(data[16:previewFrameHeaderSize], sum[:]) {
		return nil, false
	}
	return body, true
}

func (cache previewFrameCache) publish(ctx context.Context, body []byte) {
	if cache.sessionClosed() || !cache.ownsPublication() {
		return
	}
	// Mkdir (not MkdirAll) cannot resurrect a removed session owner.
	if err := os.Mkdir(cache.dir, 0o700); err != nil && !os.IsExist(err) {
		return
	}
	lock, err := os.OpenFile(filepath.Join(cache.dir, "publication.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if !platform.TryLockFile(lock) || ctx.Err() != nil {
		return
	}
	if cache.sessionClosed() {
		return
	}
	if _, ok := cache.read(); ok {
		return
	}
	if !cache.makeRoom(len(body) + previewFrameHeaderSize) {
		return
	}
	tmp, err := os.CreateTemp(cache.dir, "frame-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	var header [previewFrameHeaderSize]byte
	copy(header[:8], previewFrameMagic)
	binary.LittleEndian.PutUint64(header[8:16], uint64(len(body)))
	sum := sha256.Sum256(body)
	copy(header[16:], sum[:])
	_, headerErr := tmp.Write(header[:])
	_, bodyErr := tmp.Write(body)
	closeErr := tmp.Close()
	if headerErr != nil || bodyErr != nil || closeErr != nil || ctx.Err() != nil || cache.sessionClosed() || !cache.ownsPublication() {
		return
	}
	// Never replace an existing completed same-key artifact: it may be open
	// in another helper on Windows. Cache identity assumes stable source.
	if existing, ok := cache.read(); ok && len(existing) > 0 {
		return
	}
	_ = os.Remove(cache.path) // corrupt/evicted entry only
	_ = os.Rename(name, cache.path)
}

func (cache previewFrameCache) ownsPublication() bool {
	if claim := platform.InternalWorkClaimPath(); claim != "" {
		return platform.InternalWorkCanPublish()
	}
	if cache.claimPath == "" {
		return true
	}
	pid := readPredecessorPID(cache.claimPath)
	return pid == 0 || pid == os.Getpid()
}

func (cache previewFrameCache) sessionClosed() bool {
	if cache.dir == "" {
		return false
	}
	// Shared storage can outlive an individual checkpoint owner. A late
	// publisher must not treat its removed owner's missing marker as open.
	if _, err := os.Stat(cache.artifactOwner); err != nil {
		return true
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(cache.dir), previewSessionClosedFile))
	if err == nil {
		return true
	}
	_, err = os.Stat(filepath.Join(cache.artifactOwner, previewSessionClosedFile))
	return err == nil
}

// Share one storage budget across an interactive run's existing artifact
// owners. Children inherit the private directory; keys still include their
// immutable generation/state artifact identity. No new CLI transport is needed.
func beginPreviewFrameSession() func() {
	dir, err := os.MkdirTemp("", "catclip-preview-frames-*")
	if err != nil {
		return func() {}
	}
	previous, existed := os.LookupEnv(previewFrameSessionEnv)
	if err := os.Setenv(previewFrameSessionEnv, dir); err != nil {
		_ = os.RemoveAll(dir)
		return func() {}
	}
	return func() {
		removePreviewSession(dir)
		if existed {
			_ = os.Setenv(previewFrameSessionEnv, previous)
		} else {
			_ = os.Unsetenv(previewFrameSessionEnv)
		}
	}
}

// closePreviewSession stops tracked children before existing temp-directory
// cleanup. The marker also prevents in-flight publication while cleanup runs.
// Do not use this on a retained owner merely because one picker frame closes.
func closePreviewSession(dir string) {
	if dir == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, previewSessionClosedFile), nil, 0o600)
	picker.StopPreviewProcess(dir, predecessorBucketFilePreview)
	picker.StopPreviewProcess(dir, predecessorBucketContentMatch)
}

func removePreviewSession(dir string) {
	closePreviewSession(dir)
	_ = os.RemoveAll(dir)
}

func (cache previewFrameCache) makeRoom(incoming int) bool {
	if incoming > cache.maxBytes || cache.maxEntries < 1 {
		return false
	}
	entries, err := os.ReadDir(cache.dir)
	if err != nil {
		return false
	}
	type frame struct {
		path    string
		size    int64
		touched time.Time
	}
	var frames []frame
	var total int64
	for _, entry := range entries {
		path := filepath.Join(cache.dir, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			// Only publishers write temporaries and the kernel lock excludes
			// live writers. Remove artifacts left by terminated publishers.
			if err := os.Remove(path); err != nil {
				return false
			}
		} else if strings.HasSuffix(entry.Name(), ".frame") {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return false
			}
			frames = append(frames, frame{path, info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].touched.Before(frames[j].touched) })
	count := len(frames)
	for _, frame := range frames {
		if total+int64(incoming) <= int64(cache.maxBytes) && count < cache.maxEntries {
			break
		}
		if err := os.Remove(frame.path); err != nil {
			return false
		}
		total -= frame.size
		count--
	}
	return total+int64(incoming) <= int64(cache.maxBytes) && count < cache.maxEntries
}
