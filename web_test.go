package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebIndexHasEventFilters(t *testing.T) {
	html := webIndexHTML(nil)
	for _, s := range []string{`id="events-filters"`, `id="events-list"`, `data-tab="events"`, "Load older", `id="mask-editors"`} {
		if !strings.Contains(html, s) {
			t.Fatalf("missing %q", s)
		}
	}
}

func TestAPIEventsPagesFromJSONL(t *testing.T) {
	root := t.TempDir()
	oldAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	newAt := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	writeJSONL(t, root, []EventRecord{
		{ID: "old-alert", Type: "alert", Cam: "hik2", At: oldAt, MainClass: "person"},
		{ID: "new-alert", Type: "alert", Cam: "hik1", At: newAt, MainClass: "car"},
	})
	store, err := newEventStore(root, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", func(rw http.ResponseWriter, r *http.Request) {
		limit := 100
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				if n > 500 {
					n = 500
				}
				limit = n
			}
		}
		writeJSON(rw, store.queryEvents(eventQuery{
			Type:   r.URL.Query().Get("type"),
			Cam:    r.URL.Query().Get("cam"),
			Class:  r.URL.Query().Get("class"),
			Before: parseQueryTime(r.URL.Query().Get("before")),
			Since:  parseQueryTime(r.URL.Query().Get("since")),
			Until:  parseQueryTime(r.URL.Query().Get("until")),
			Limit:  limit,
		}))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	get := func(q string) eventPage {
		t.Helper()
		resp, err := http.Get(ts.URL + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var page eventPage
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	all := get("/api/events?type=alert")
	if all.Total != 2 || len(all.Events) != 2 || all.Events[0].ID != "new-alert" {
		t.Fatalf("all alerts: %+v", all)
	}
	hik2 := get("/api/events?type=alert&cam=hik2")
	if hik2.Total != 1 || hik2.Events[0].ID != "old-alert" {
		t.Fatalf("cam filter: %+v", hik2)
	}
	first := get("/api/events?type=alert&limit=1")
	if !first.HasMore || first.Events[0].ID != "new-alert" {
		t.Fatalf("limit page: %+v", first)
	}
	older := get("/api/events?type=alert&limit=1&before=" + first.Events[0].At.Format(time.RFC3339Nano))
	if older.Events[0].ID != "old-alert" || older.HasMore {
		t.Fatalf("before cursor: %+v", older)
	}
}
