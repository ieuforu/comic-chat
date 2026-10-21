package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryAPIPagesBackwards(t *testing.T) {
	history, _ := newTestHistory(t, 0)
	t.Cleanup(func() { _ = history.Close() })

	for i := uint64(1); i <= 5; i++ {
		history.Append(msg(i, "m"))
	}

	waitForHistory(t, history, 5)

	srv := httptest.NewServer(Routes(Deps{
		Hub:      NewHub(),
		Sessions: newTestStore(t),
		History:  history,
		StaticFS: testStaticFS(),
	}))
	t.Cleanup(srv.Close)

	cookie := newSession(t, srv)

	get := func(path string, withCookie bool) *http.Response {
		t.Helper()

		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		if withCookie {
			req.AddCookie(cookie)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}

		t.Cleanup(func() { resp.Body.Close() })

		return resp
	}

	if resp := get("/api/history?before=5", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without session status = %d, want 401", resp.StatusCode)
	}

	if resp := get("/api/history?before=0", true); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("before=0 status = %d, want 400", resp.StatusCode)
	}

	if resp := get("/api/history?before=4&limit=nope", true); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad limit status = %d, want 400", resp.StatusCode)
	}

	first := get("/api/history?before=5&limit=2", true)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first page status = %d", first.StatusCode)
	}

	var page historyPage
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatalf("decode first page: %v", err)
	}

	if len(page.Messages) != 2 || page.Messages[0].Seq != 3 || page.Messages[1].Seq != 4 {
		t.Fatalf("first page = %+v, want seqs [3 4]", page.Messages)
	}

	if !page.HasMore {
		t.Fatal("first page should report more history")
	}

	second := get("/api/history?before=3&limit=2", true)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second page status = %d", second.StatusCode)
	}

	if err := json.NewDecoder(second.Body).Decode(&page); err != nil {
		t.Fatalf("decode second page: %v", err)
	}

	if len(page.Messages) != 2 || page.Messages[0].Seq != 1 || page.Messages[1].Seq != 2 {
		t.Fatalf("second page = %+v, want seqs [1 2]", page.Messages)
	}

	if page.HasMore {
		t.Fatal("second page should be the oldest")
	}

	// An oversized limit is clamped inside LoadBefore, not trusted by the handler.
	huge := get("/api/history?before=5&limit=1000", true)
	if huge.StatusCode != http.StatusOK {
		t.Fatalf("huge limit status = %d", huge.StatusCode)
	}

	if err := json.NewDecoder(huge.Body).Decode(&page); err != nil {
		t.Fatalf("decode huge page: %v", err)
	}

	if len(page.Messages) != 4 || page.HasMore {
		t.Fatalf("huge page = %d messages, hasMore=%v", len(page.Messages), page.HasMore)
	}
}
