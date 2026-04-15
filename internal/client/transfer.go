package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Allergic2Bulletz/wani/internal/protocol"
	"github.com/cespare/xxhash/v2"
	"github.com/quic-go/quic-go"
)

// resumeState tracks which files have been successfully received in a previous run.
type resumeState struct {
	Completed []string `json:"completed"`
}

func resumeStatePath(destDir string) string {
	return filepath.Join(destDir, ".wani-resume.json")
}

func loadResumeState(destDir string) resumeState {
	data, err := os.ReadFile(resumeStatePath(destDir))
	if err != nil {
		return resumeState{}
	}
	var s resumeState
	if err := json.Unmarshal(data, &s); err != nil {
		return resumeState{}
	}
	return s
}

func saveResumeState(destDir string, state resumeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("client.saveResumeState: marshal: %w", err)
	}
	if err := os.WriteFile(resumeStatePath(destDir), data, 0o600); err != nil {
		return fmt.Errorf("client.saveResumeState: write: %w", err)
	}
	return nil
}

func deleteResumeState(destDir string) {
	os.Remove(resumeStatePath(destDir)) //nolint:errcheck
}

// ProgressFunc is called after each chunk written during file transfer.
// bytesWritten is the running total transferred so far for this file.
type ProgressFunc func(path string, bytesWritten, totalBytes int64)

// SendManifest sends the manifest to the receiver over a new QUIC stream and waits for
// a ManifestResponse before returning. The caller must not use this stream afterwards.
func SendManifest(ctx context.Context, conn *quic.Conn, manifest protocol.Manifest) (protocol.ManifestResponse, error) {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return protocol.ManifestResponse{}, fmt.Errorf("client.SendManifest: OpenStream: %w", err)
	}
	defer stream.Close()

	enc := json.NewEncoder(stream)
	if err := enc.Encode(manifest); err != nil {
		return protocol.ManifestResponse{}, fmt.Errorf("client.SendManifest: encode: %w", err)
	}
	// Close the write side to signal EOF; the receiver can still reply.
	if err := stream.Close(); err != nil {
		return protocol.ManifestResponse{}, fmt.Errorf("client.SendManifest: close write: %w", err)
	}

	var resp protocol.ManifestResponse
	dec := json.NewDecoder(stream)
	if err := dec.Decode(&resp); err != nil {
		return protocol.ManifestResponse{}, fmt.Errorf("client.SendManifest: decode response: %w", err)
	}
	return resp, nil
}

// ReceiveManifest accepts the manifest stream from the sender, creates required directories
// under destDir, and sends a ManifestResponse indicating readiness. Returns the received manifest
// and the list of already-completed file paths (from a previous partial transfer).
func ReceiveManifest(ctx context.Context, conn *quic.Conn, destDir string) (protocol.Manifest, []string, error) {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return protocol.Manifest{}, nil, fmt.Errorf("client.ReceiveManifest: AcceptStream: %w", err)
	}
	defer stream.Close()

	var manifest protocol.Manifest
	dec := json.NewDecoder(stream)
	if err := dec.Decode(&manifest); err != nil {
		return protocol.Manifest{}, nil, fmt.Errorf("client.ReceiveManifest: decode: %w", err)
	}

	// Pre-create all destination directories.
	for _, entry := range manifest.Files {
		dir := filepath.Join(destDir, filepath.FromSlash(filepath.Dir(entry.Path)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return protocol.Manifest{}, nil, fmt.Errorf("client.ReceiveManifest: mkdir %s: %w", dir, err)
		}
	}

	state := loadResumeState(destDir)
	completed := state.Completed
	if completed == nil {
		completed = []string{}
	}
	resp := protocol.ManifestResponse{
		Status:    "ready",
		Completed: completed,
	}
	enc := json.NewEncoder(stream)
	if err := enc.Encode(resp); err != nil {
		return protocol.Manifest{}, nil, fmt.Errorf("client.ReceiveManifest: encode response: %w", err)
	}

	return manifest, completed, nil
}

// SendFiles sends each file in manifest sequentially — one QUIC stream per file.
// Files listed in resp.Completed are skipped (already received). progress may be nil.
func SendFiles(ctx context.Context, conn *quic.Conn, manifest protocol.Manifest, resp protocol.ManifestResponse, root string, progress ProgressFunc) error {
	done := make(map[string]bool, len(resp.Completed))
	for _, p := range resp.Completed {
		done[p] = true
	}
	for _, entry := range manifest.Files {
		if done[entry.Path] {
			if progress != nil {
				progress(entry.Path, entry.Size, entry.Size)
			}
			continue
		}
		if err := sendFile(ctx, conn, root, entry, progress); err != nil {
			return err
		}
	}
	return nil
}

func sendFile(ctx context.Context, conn *quic.Conn, root string, entry protocol.FileEntry, progress ProgressFunc) error {
	// When root is a single file (not a directory), root itself is what we open.
	// filepath.Join(root, entry.Path) would double-append the basename.
	fullPath := filepath.Join(root, filepath.FromSlash(entry.Path))
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		fullPath = root
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return fmt.Errorf("client.sendFile open %s: %w", entry.Path, err)
	}
	defer f.Close()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("client.sendFile %s: OpenStream: %w", entry.Path, err)
	}
	defer stream.Close()

	var src io.Reader = f
	if progress != nil {
		src = &progressReader{r: f, path: entry.Path, total: entry.Size, progress: progress}
	}

	if _, err := io.Copy(stream, src); err != nil {
		return fmt.Errorf("client.sendFile %s: copy: %w", entry.Path, err)
	}
	// Close write side so receiver sees EOF.
	if err := stream.Close(); err != nil {
		return fmt.Errorf("client.sendFile %s: close: %w", entry.Path, err)
	}

	// Read receiver's ack ("ok" or error message).
	ack, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("client.sendFile %s: read ack: %w", entry.Path, err)
	}
	if string(ack) != "ok" {
		return fmt.Errorf("client.sendFile %s: receiver error: %s", entry.Path, ack)
	}
	return nil
}

// ReceiveFiles accepts one QUIC stream per file, writes each to destDir, and verifies
// the xxHash-64 digest against the manifest. Files listed in skip are counted as already done.
// progress may be nil. Clears the resume state on full success.
func ReceiveFiles(ctx context.Context, conn *quic.Conn, manifest protocol.Manifest, skip map[string]bool, destDir string, progress ProgressFunc) error {
	for _, entry := range manifest.Files {
		if skip[entry.Path] {
			if progress != nil {
				progress(entry.Path, entry.Size, entry.Size)
			}
			continue
		}
		if err := receiveFile(ctx, conn, destDir, entry, progress); err != nil {
			return err
		}
	}
	deleteResumeState(destDir)
	return nil
}

func receiveFile(ctx context.Context, conn *quic.Conn, destDir string, entry protocol.FileEntry, progress ProgressFunc) error {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return fmt.Errorf("client.receiveFile %s: AcceptStream: %w", entry.Path, err)
	}
	defer stream.Close()

	destPath := filepath.Join(destDir, filepath.FromSlash(entry.Path))
	f, err := os.Create(destPath)
	if err != nil {
		stream.Write([]byte(fmt.Sprintf("create failed: %s", err))) //nolint:errcheck
		return fmt.Errorf("client.receiveFile %s: create: %w", entry.Path, err)
	}
	defer f.Close()

	h := xxhash.New()
	dst := io.MultiWriter(f, h)

	var src io.Reader = stream
	if progress != nil {
		src = &progressReader{r: stream, path: entry.Path, total: entry.Size, progress: progress}
	}

	if _, err := io.Copy(dst, src); err != nil {
		stream.Write([]byte(fmt.Sprintf("copy failed: %s", err))) //nolint:errcheck
		return fmt.Errorf("client.receiveFile %s: copy: %w", entry.Path, err)
	}

	if h.Sum64() != entry.XXHash {
		msg := fmt.Sprintf("xxHash mismatch for %s", entry.Path)
		stream.Write([]byte(msg)) //nolint:errcheck
		return fmt.Errorf("client.receiveFile: %s", msg)
	}

	if _, err := stream.Write([]byte("ok")); err != nil {
		return fmt.Errorf("client.receiveFile %s: write ack: %w", entry.Path, err)
	}

	// Persist progress so an interrupted transfer can resume.
	s := loadResumeState(destDir)
	s.Completed = append(s.Completed, entry.Path)
	saveResumeState(destDir, s) //nolint:errcheck

	return nil
}

// progressReader wraps an io.Reader and invokes progress after each read.
type progressReader struct {
	r        io.Reader
	path     string
	total    int64
	written  int64
	progress ProgressFunc
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.written += int64(n)
	if pr.progress != nil {
		pr.progress(pr.path, pr.written, pr.total)
	}
	return n, err
}
