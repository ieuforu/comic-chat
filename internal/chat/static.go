package chat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
)

// StaticBundle is the embedded SPA prepared for serving: the build id identifies the
// exact asset set, and IndexHTML already carries it as a meta tag so an open page can
// tell whether the server it reconnects to is the build it loaded.
type StaticBundle struct {
	BuildID   string
	IndexHTML []byte
}

// LoadStatic hashes every file in the embedded frontend and injects the resulting
// build id into index.html.
//
// The id is derived from the bytes that are actually embedded, not from a git SHA:
// a dirty or untagged build still gets a distinct id, and two processes serving the
// same binary always agree.
func LoadStatic(fsys fs.FS) (*StaticBundle, error) {
	sum := sha256.New()

	var indexHTML []byte

	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}

		// Length-prefixed fields keep the hash unambiguous even if a file contains
		// the separator byte.
		writeHashField(sum, []byte(path))
		writeHashField(sum, data)

		if path == "index.html" {
			indexHTML = data
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	buildID := hex.EncodeToString(sum.Sum(nil)[:8])

	return &StaticBundle{
		BuildID:   buildID,
		IndexHTML: injectBuildID(indexHTML, buildID),
	}, nil
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashField(w hashWriter, value []byte) {
	var length [8]byte

	n := uint64(len(value))
	for i := range length {
		length[i] = byte(n >> (8 * (len(length) - 1 - i)))
	}

	_, _ = w.Write(length[:])
	_, _ = w.Write(value)
}

// injectBuildID puts a meta tag in the document head. The page reads it once at
// load, so an old bundle keeps the old id even after the server has moved on.
func injectBuildID(indexHTML []byte, buildID string) []byte {
	if len(indexHTML) == 0 {
		return nil
	}

	meta := []byte(`<meta name="app-build" content="` + buildID + `" />`)

	headEnd := bytes.Index(indexHTML, []byte("</head>"))
	if headEnd < 0 {
		return append(meta, indexHTML...)
	}

	out := make([]byte, 0, len(indexHTML)+len(meta))
	out = append(out, indexHTML[:headEnd]...)
	out = append(out, meta...)
	out = append(out, indexHTML[headEnd:]...)

	return out
}
