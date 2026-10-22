package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func adminDelete(t *testing.T, srv *httptest.Server, token string, seq uint64) *http.Response {
	t.Helper()

	body, _ := json.Marshal(map[string]uint64{"seq": seq})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/delete", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin delete: %v", err)
	}

	return resp
}

func TestAdminDeleteRemovesMessageAndBroadcasts(t *testing.T) {
	history, _ := newTestHistory(t, 0)
	t.Cleanup(func() { _ = history.Close() })

	history.Append(msg(1, "delete me"))
	waitForHistory(t, history, 1)

	hub := NewHub()
	hub.SetHistorySink(history)
	go hub.Run()
	t.Cleanup(hub.Shutdown)

	srv := httptest.NewServer(Routes(Deps{
		Hub:        hub,
		Sessions:   newTestStore(t),
		History:    history,
		AdminToken: "secret",
		StaticFS:   testStaticFS(),
	}))
	t.Cleanup(srv.Close)

	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	if resp := adminDelete(t, srv, "wrong", 1); resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()

		t.Fatalf("wrong token status = %d, want 401", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	if resp := adminDelete(t, srv, "secret", 1); resp.StatusCode != http.StatusOK {
		resp.Body.Close()

		t.Fatalf("delete status = %d, want 200", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	deleted := readUntil(t, conn, "delete")
	if deleted.Seq != 1 {
		t.Fatalf("delete seq = %d, want 1", deleted.Seq)
	}

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		if _, ok, err := history.Message(1); err == nil && !ok {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("message was not deleted from storage")
}

func TestAdminDeleteDisabledWithoutToken(t *testing.T) {
	srv := httptest.NewServer(Routes(Deps{
		Hub:      NewHub(),
		Sessions: newTestStore(t),
		StaticFS: testStaticFS(),
	}))
	t.Cleanup(srv.Close)

	if resp := adminDelete(t, srv, "secret", 1); resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()

		t.Fatalf("disabled admin status = %d, want 404", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}
