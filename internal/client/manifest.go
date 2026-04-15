package client

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Allergic2Bulletz/wani/internal/protocol"
	"github.com/cespare/xxhash/v2"
)

// BuildManifest walks root (a file or directory) and returns a Manifest listing
// every regular file with its relative path, size, and xxHash-64 digest.
// Symlinks and irregular files are skipped without error.
func BuildManifest(root string) (protocol.Manifest, error) {
	info, err := os.Stat(root)
	if err != nil {
		return protocol.Manifest{}, fmt.Errorf("client.BuildManifest: stat: %w", err)
	}

	var entries []protocol.FileEntry

	if !info.IsDir() {
		// Single file: the relative path inside the manifest is just the base name.
		entry, err := fileEntry(root, info.Name())
		if err != nil {
			return protocol.Manifest{}, err
		}
		entries = append(entries, entry)
		return protocol.Manifest{Files: entries}, nil
	}

	// Directory: walk and record every regular file.
	manifest := protocol.Manifest{RootName: filepath.Base(root)}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil // skip dirs, symlinks, etc.
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		// Normalize to forward slashes for cross-platform portability.
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("info %s: %w", path, err)
		}
		entry, err := fileEntry(path, rel)
		if err != nil {
			return err
		}
		_ = fi // size already inside fileEntry
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return protocol.Manifest{}, fmt.Errorf("client.BuildManifest: walk: %w", err)
	}

	manifest.Files = entries
	return manifest, nil
}

// fileEntry opens path, reads its full contents to compute the xxHash-64 digest,
// and returns a FileEntry with relPath, size, and hash.
func fileEntry(path, relPath string) (protocol.FileEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return protocol.FileEntry{}, fmt.Errorf("client.fileEntry open %s: %w", path, err)
	}
	defer f.Close()

	h := xxhash.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return protocol.FileEntry{}, fmt.Errorf("client.fileEntry hash %s: %w", path, err)
	}

	return protocol.FileEntry{
		Path:        relPath,
		Size:        n,
		XXHash:      h.Sum64(),
		Compression: "none",
	}, nil
}
