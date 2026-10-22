package chat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// pngBytes builds a tiny valid PNG, so the sniffer has something real to detect.
func pngBytes(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	return buf.Bytes()
}

func upload(t *testing.T, srv *httptest.Server, cookie *http.Cookie, field string, data []byte, contentType string) (*http.Response, map[string]any) {
	t.Helper()

	var body bytes.Buffer

	writer := multipart.NewWriter(&body)

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="x"`)
	header.Set("Content-Type", contentType)

	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}

	writer.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/upload", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if cookie != nil {
		req.AddCookie(cookie)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	parsed := map[string]any{}
	_ = json.Unmarshal(raw, &parsed)

	return resp, parsed
}

func TestUploadRequiresSession(t *testing.T) {
	srv, _, _ := newTestServer(t)

	resp, _ := upload(t, srv, nil, "file", pngBytes(t), "image/png")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUploadAcceptsPngAndServesIt(t *testing.T) {
	srv, _, store := newTestServer(t)

	cookie := newSession(t, srv)
	data := pngBytes(t)

	resp, body := upload(t, srv, cookie, "file", data, "image/png")

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%v)", resp.StatusCode, body)
	}

	id, _ := body["id"].(string)
	if !validMediaID(id) {
		t.Fatalf("id = %v", body["id"])
	}

	if body["mime"] != "image/png" {
		t.Fatalf("mime = %v", body["mime"])
	}

	// Serving must set a fixed content type and forbid re-interpretation.
	get, err := http.Get(srv.URL + "/media/" + id)
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	defer get.Body.Close()

	if get.StatusCode != http.StatusOK {
		t.Fatalf("media status = %d", get.StatusCode)
	}

	if ct := get.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}

	if get.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}

	served, _ := io.ReadAll(get.Body)
	if !bytes.Equal(served, data) {
		t.Fatal("served bytes differ from uploaded bytes")
	}

	_ = store
}

func TestUploadRejectsDisguisedContent(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cookie := newSession(t, srv)

	// A script pretending to be a PNG: the sniffer must catch it.
	resp, body := upload(t, srv, cookie, "file", []byte("<script>alert(1)</script>"), "image/png")

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (%v)", resp.StatusCode, body)
	}
}

func TestUploadRejectsSvg(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cookie := newSession(t, srv)

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	resp, _ := upload(t, srv, cookie, "file", svg, "image/svg+xml")

	if resp.StatusCode == http.StatusCreated {
		t.Fatal("SVG must not be accepted: it can carry script")
	}
}

func TestUploadRejectsOversizedFile(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cookie := newSession(t, srv)

	// A PNG header followed by megabytes of padding still sniffs as PNG, so this
	// exercises the size cap rather than the type check.
	huge := append(pngBytes(t), make([]byte, 6<<20)...)

	resp, _ := upload(t, srv, cookie, "file", huge, "image/png")

	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 413/400", resp.StatusCode)
	}
}

func TestMediaExpiredAfterTTL(t *testing.T) {
	srv, _, _, media := newTestServerWithMedia(t)
	cookie := newSession(t, srv)

	resp, body := upload(t, srv, cookie, "file", pngBytes(t), "image/png")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed: %v", body)
	}

	id := body["id"].(string)
	if !validMediaID(id) {
		t.Fatalf("id = %v", body["id"])
	}

	// Age the row past the TTL, then prune.
	if _, err := media.db.Exec(`UPDATE media SET created_at = ?`, time.Now().Add(-8*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	removed, err := media.Prune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if removed != 1 {
		t.Fatalf("pruned %d rows, want 1", removed)
	}
}

func TestImageMessageRequiresOwnership(t *testing.T) {
	srv, _, store := newTestServer(t)

	owner := newSession(t, srv)
	resp, body := upload(t, srv, owner, "file", pngBytes(t), "image/png")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed: %v", body)
	}

	id := body["id"].(string)

	// A different session must not be able to post a message referencing it.
	stranger := newSession(t, srv)
	conn := dialWith(t, srv, stranger)
	readUntil(t, conn, "snapshot")

	payload := `{"type":"image","content":"` + id + `"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Nothing should come back; a chat message proves the frame was dropped.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"probe"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readUntil(t, conn, "chat")

	if msg.Content != "probe" {
		t.Fatalf("unexpected message %+v", msg)
	}

	_ = store
}

func TestImageMessageIsBroadcastAndPersisted(t *testing.T) {
	srv, _, store := newTestServer(t)

	owner := newSession(t, srv)
	resp, body := upload(t, srv, owner, "file", pngBytes(t), "image/png")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed: %v", body)
	}

	id := body["id"].(string)

	conn := dialWith(t, srv, owner)
	readUntil(t, conn, "snapshot")

	payload := `{"type":"image","content":"` + id + `"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readUntil(t, conn, "image")

	if msg.Content != id {
		t.Fatalf("content = %q, want the media id", msg.Content)
	}

	if msg.Seq == 0 {
		t.Fatal("image messages must get a sequence number")
	}

	// A late joiner must see it in the snapshot, i.e. it was persisted.
	late := newSession(t, srv)
	lateConn := dialWith(t, srv, late)
	snapshot := readUntil(t, lateConn, "snapshot")

	found := false

	for _, item := range snapshot.History {
		if item.Type == "image" && item.Content == id {
			found = true
		}
	}

	if !found {
		t.Fatalf("image message missing from replayed history: %+v", snapshot.History)
	}

	_ = store
}

func TestMediaStoreWithoutDB(t *testing.T) {
	// Guard the "images disabled" path: a nil MediaStore must not panic the router.
	mux := http.NewServeMux()
	mediaRoutes(mux, nil, NewSessionStore(t.TempDir()))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/upload", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}

var _ = sql.ErrNoRows

func TestMediaDeleteRemovesFileAndRow(t *testing.T) {
	media := newTestMedia(t, newTestStore(t))

	id, _, _, err := media.Save("session-1", bytes.NewReader(pngBytes(t)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := media.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, _, err := media.Open(id); err == nil {
		t.Fatal("Open after Delete should fail")
	}
}
