package chat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// MediaStore keeps uploaded images on disk and their metadata in SQLite.
//
// Images never travel over the WebSocket: every frame is capped at 512 bytes, and
// pushing megabytes through the broadcast fan-out would defeat the whole design.
// The client uploads over HTTP, gets an id back, and then sends a tiny `image`
// message that references it.
type MediaStore struct {
	db  *sql.DB
	dir string
	// maxBytes bounds a single upload; ttl bounds how long files are kept.
	maxBytes int64
	ttl      time.Duration
}

// allowedMimes is an allowlist rather than a blocklist: image/svg+xml can carry
// script and would be an XSS vector, so it is simply not accepted.
var allowedMimes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

const (
	defaultMaxMediaBytes = 5 << 20 // 5 MB
	defaultMediaTTL      = 7 * 24 * time.Hour
)

// MediaError carries an HTTP status so handlers can translate it directly.
type MediaError struct {
	Status  int
	Message string
}

func (e *MediaError) Error() string { return e.Message }

// NewMediaStore prepares the media table and directory. A missing directory is
// created; a failure is returned so the caller can decide whether to run without
// images.
func NewMediaStore(db *sql.DB, dir string) (*MediaStore, error) {
	maxBytes := int64(defaultMaxMediaBytes)
	if raw := os.Getenv("MEDIA_MAX_MB"); raw != "" {
		if mb, err := strconv.Atoi(raw); err == nil && mb > 0 {
			maxBytes = int64(mb) << 20
		} else {
			log.Printf("警告: MEDIA_MAX_MB=%q 无效，使用 %d MB", raw, defaultMaxMediaBytes>>20)
		}
	}

	ttl := defaultMediaTTL
	if raw := os.Getenv("MEDIA_TTL_HOURS"); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 {
			ttl = time.Duration(hours) * time.Hour
		} else {
			log.Printf("警告: MEDIA_TTL_HOURS=%q 无效，使用 %d 天", raw, int(defaultMediaTTL.Hours()/24))
		}
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建媒体目录: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media (
			id         TEXT PRIMARY KEY,
			session_id TEXT    NOT NULL,
			mime       TEXT    NOT NULL,
			size       INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS media_created_at ON media(created_at);
	`); err != nil {
		return nil, fmt.Errorf("创建 media 表: %w", err)
	}

	store := &MediaStore{db: db, dir: dir, maxBytes: maxBytes, ttl: ttl}

	if removed, err := store.Prune(); err != nil {
		log.Printf("警告: 清理过期图片失败: %v", err)
	} else if removed > 0 {
		log.Printf("已清理 %d 张过期图片（保留 %d 天）", removed, int(ttl.Hours()/24))
	}

	go store.pruneLoop()

	return store, nil
}

// MaxBytes is the per-upload limit, for the handler's request cap.
func (s *MediaStore) MaxBytes() int64 { return s.maxBytes }

// MaxMB and TTLDays are for the startup log line.
func (s *MediaStore) MaxMB() int64 { return s.maxBytes >> 20 }

func (s *MediaStore) TTLDays() int { return int(s.ttl.Hours() / 24) }

// Save validates and stores one uploaded image. It sniffs the content instead of
// trusting the multipart content type.
func (s *MediaStore) Save(sessionID string, r io.Reader) (string, string, int64, error) {
	// Read one byte past the cap so an oversized upload is detected rather than
	// silently truncated.
	limited := io.LimitReader(r, s.maxBytes+1)

	data, err := io.ReadAll(limited)
	if err != nil {
		return "", "", 0, &MediaError{Status: http.StatusBadRequest, Message: "读取上传内容失败"}
	}

	size := int64(len(data))

	if size == 0 {
		return "", "", 0, &MediaError{Status: http.StatusBadRequest, Message: "空文件"}
	}

	if size > s.maxBytes {
		return "", "", 0, &MediaError{
			Status:  http.StatusRequestEntityTooLarge,
			Message: fmt.Sprintf("图片不能超过 %d MB", s.maxBytes>>20),
		}
	}

	// Sniff the bytes instead of trusting the multipart content type: a script
	// renamed to .png still sniffs as text.
	sniffLen := min(len(data), 512)
	detected := http.DetectContentType(data[:sniffLen])

	ext, ok := allowedMimes[detected]
	if !ok {
		return "", "", 0, &MediaError{
			Status:  http.StatusUnsupportedMediaType,
			Message: fmt.Sprintf("只支持 PNG / JPEG / GIF / WebP（识别到 %s）", detected),
		}
	}

	id, err := randomID()
	if err != nil {
		return "", "", 0, err
	}

	path := filepath.Join(s.dir, id+ext)
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return "", "", 0, fmt.Errorf("写入图片失败: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck // best effort
		return "", "", 0, fmt.Errorf("落盘图片失败: %w", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO media (id, session_id, mime, size, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, sessionID, detected, size, time.Now().UnixMilli(),
	); err != nil {
		os.Remove(path) //nolint:errcheck // best effort
		return "", "", 0, fmt.Errorf("记录图片失败: %w", err)
	}

	return id, detected, size, nil
}

// Open returns the file and mime type for an id, or a 404 MediaError.
func (s *MediaStore) Open(id string) (io.ReadCloser, string, error) {
	var (
		mime      string
		createdAt int64
	)

	err := s.db.QueryRow(`SELECT mime, created_at FROM media WHERE id = ?`, id).Scan(&mime, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", &MediaError{Status: http.StatusNotFound, Message: "图片不存在或已过期"}
	}

	if err != nil {
		return nil, "", err
	}

	if s.expired(createdAt) {
		return nil, "", &MediaError{Status: http.StatusNotFound, Message: "图片已过期"}
	}

	ext := allowedMimes[mime]

	file, err := os.Open(filepath.Join(s.dir, id+ext))
	if err != nil {
		// Row exists but the file is gone: treat as expired rather than a 500.
		return nil, "", &MediaError{Status: http.StatusNotFound, Message: "图片不存在或已过期"}
	}

	return file, mime, nil
}

// Owns reports whether the session uploaded this image. Used to reject an image
// message that references somebody else's upload.
func (s *MediaStore) Owns(id, sessionID string) bool {
	var owner string

	if err := s.db.QueryRow(`SELECT session_id FROM media WHERE id = ?`, id).Scan(&owner); err != nil {
		return false
	}

	return owner == sessionID
}

// Prune deletes files and rows past the TTL. Chat text is kept forever; only the
// binaries age out, which is what keeps a small disk from filling up.
func (s *MediaStore) Prune() (int, error) {
	cutoff := time.Now().Add(-s.ttl).UnixMilli()

	rows, err := s.db.Query(`SELECT id, mime FROM media WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}

	type victim struct{ id, mime string }

	var victims []victim

	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.mime); err != nil {
			rows.Close()

			return 0, err
		}

		victims = append(victims, v)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, v := range victims {
		os.Remove(filepath.Join(s.dir, v.id+allowedMimes[v.mime])) //nolint:errcheck // best effort
	}

	if len(victims) > 0 {
		if _, err := s.db.Exec(`DELETE FROM media WHERE created_at < ?`, cutoff); err != nil {
			return 0, err
		}
	}

	return len(victims), nil
}

func (s *MediaStore) expired(createdAtMillis int64) bool {
	return time.Since(time.UnixMilli(createdAtMillis)) > s.ttl
}

func (s *MediaStore) pruneLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if removed, err := s.Prune(); err != nil {
			log.Printf("警告: 清理过期图片失败: %v", err)
		} else if removed > 0 {
			log.Printf("已清理 %d 张过期图片", removed)
		}
	}
}

func randomID() (string, error) {
	buf := make([]byte, 16)

	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}

// validMediaID guards the id before it is used in a filesystem path.
func validMediaID(id string) bool {
	if len(id) != 32 {
		return false
	}

	_, err := hex.DecodeString(id)

	return err == nil
}

// mediaRoutes mounts upload and download. Both require a session, because an
// anonymous uploader would be an open file host on a public server.
func mediaRoutes(mux *http.ServeMux, media *MediaStore, sessions *SessionStore) {
	if media == nil {
		return
	}

	mux.HandleFunc("POST /api/upload", func(w http.ResponseWriter, r *http.Request) {
		profile, ok := sessions.readProfile(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})

			return
		}

		// Cap the whole request: multipart framing adds a little on top of the file.
		r.Body = http.MaxBytesReader(w, r.Body, media.MaxBytes()+1<<20)

		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 file 字段"})

			return
		}
		defer file.Close()

		id, mime, size, err := media.Save(profile.SessionID, file)
		if err != nil {
			var mediaErr *MediaError
			if errors.As(err, &mediaErr) {
				writeJSON(w, mediaErr.Status, map[string]string{"error": mediaErr.Message})

				return
			}

			log.Printf("上传失败: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "上传失败"})

			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":   id,
			"url":  "/media/" + id,
			"mime": mime,
			"size": size,
		})
	})

	mux.HandleFunc("GET /media/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		if !validMediaID(id) {
			http.NotFound(w, r)

			return
		}

		file, mime, err := media.Open(id)
		if err != nil {
			var mediaErr *MediaError
			if errors.As(err, &mediaErr) {
				http.Error(w, mediaErr.Message, mediaErr.Status)

				return
			}

			http.Error(w, "读取失败", http.StatusInternalServerError)

			return
		}
		defer file.Close()

		// Never let the browser re-interpret the body: an uploaded file must not be
		// able to become HTML or script in this origin.
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "private, max-age=86400")

		if _, err := io.Copy(w, file); err != nil {
			log.Printf("传输图片失败: %v", err)
		}
	})
}
