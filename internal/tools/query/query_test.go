package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestElasticsearchQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/_search") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := map[string]any{
			"took":      1,
			"timed_out": false,
			"hits": map[string]any{
				"total": map[string]any{"value": 1, "relation": "eq"},
				"hits":  []any{map[string]any{"_index": "testindex", "_id": "1", "_source": map[string]any{"message": "hello world"}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	os.Setenv("ES_URL", srv.URL)
	defer os.Unsetenv("ES_URL")

	ctx := context.Background()
	params := map[string]any{"backend": "elasticsearch", "index": "testindex", "query": "hello", "size": 5}
	res, err := RunQuery(ctx, params)
	if err != nil {
		t.Fatalf("RunQuery error: %v", err)
	}
	if res["total"].(int) != 1 {
		t.Fatalf("expected total=1, got %v", res["total"])
	}
	hits := res["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
}

func TestSplunkQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/services/search/jobs/export" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// Return two JSON lines as Splunk export does
		w.Write([]byte("{\"result\":{\"_raw\":\"event1\",\"host\":\"h1\"}}\n"))
		w.Write([]byte("{\"result\":{\"_raw\":\"event2\",\"host\":\"h2\"}}\n"))
	}))
	defer srv.Close()

	os.Setenv("SPLUNK_URL", srv.URL)
	defer os.Unsetenv("SPLUNK_URL")

	ctx := context.Background()
	params := map[string]any{"backend": "splunk", "query": "index=main error", "count": 2}
	res, err := RunQuery(ctx, params)
	if err != nil {
		t.Fatalf("RunQuery splunk error: %v", err)
	}
	if res["count"].(int) != 2 {
		t.Fatalf("expected count=2, got %v", res["count"])
	}
	if arr, ok := res["results"].([]map[string]any); ok {
		if len(arr) != 2 {
			t.Fatalf("expected 2 results, got %d", len(arr))
		}
	}
}
