package protocol

// Compression identifies the per-file compression algorithm (reserved; only "none" for MVP).
type Compression string

const CompressionNone Compression = "none"

// FileEntry describes a single file in a transfer manifest.
type FileEntry struct {
	Path        string      `json:"path"`        // forward-slash relative path
	Size        int64       `json:"size"`        // bytes
	XXHash      uint64      `json:"xxhash"`      // xxhash.Sum64 of file content
	Compression Compression `json:"compression"` // always "none" for MVP
}

// Manifest is the file inventory sent by the sender before any data transfer.
type Manifest struct {
	Files    []FileEntry `json:"files"`
	RootName string      `json:"root_name,omitempty"` // base name of the source directory; empty for single-file sends
}

// ManifestResponse is the receiver's acknowledgement after parsing the manifest.
// Completed lists relative paths of files already received in a prior session (resume).
type ManifestResponse struct {
	Status    string   `json:"status"`    // "ready"
	Completed []string `json:"completed"` // paths already done (populated in 3d)
}
