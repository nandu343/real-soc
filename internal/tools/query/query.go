package query

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// RunQuery executes a query against either Elasticsearch or Splunk.
// Supported params:
// - backend: "elasticsearch" | "splunk" (optional; autodetected from env)
// - query: the query string (required)
// - index: (elasticsearch) index name
// - size/count: max results
// - es_url / splunk_url: override endpoints (optional)
// - es_username / es_password / splunk_token: auth overrides
func RunQuery(ctx context.Context, params map[string]any) (map[string]any, error) {
	backend := strings.ToLower(strings.TrimSpace(getString(params["backend"])))
	queryStr := getString(params["query"])
	if queryStr == "" {
		return nil, errors.New("missing 'query' parameter")
	}

	// autodetect backend if not provided
	if backend == "" {
		if os.Getenv("ES_URL") != "" {
			backend = "elasticsearch"
		} else if os.Getenv("SPLUNK_URL") != "" {
			backend = "splunk"
		} else {
			return nil, errors.New("no backend specified and neither ES_URL nor SPLUNK_URL are configured")
		}
	}

	switch backend {
	case "elasticsearch", "es":
		return runElasticsearchQuery(ctx, params, queryStr)
	case "splunk":
		return runSplunkQuery(ctx, params, queryStr)
	default:
		return nil, fmt.Errorf("unsupported backend: %s", backend)
	}
}

// ---------- Elasticsearch (simple REST) ---------------------------------

func runElasticsearchQuery(ctx context.Context, params map[string]any, queryStr string) (map[string]any, error) {
	index := getString(params["index"])
	if index == "" {
		return nil, errors.New("'index' parameter is required for elasticsearch backend")
	}

	esURL := getString(params["es_url"])
	if esURL == "" {
		esURL = os.Getenv("ES_URL")
		if esURL == "" {
			esURL = "http://localhost:9200"
		}
	}

	size := getInt(params["size"], 10)

	body := map[string]any{
		"query": map[string]any{"query_string": map[string]any{"query": queryStr}},
		"size":  size,
	}
	data, _ := json.Marshal(body)

	base := strings.TrimRight(esURL, "/")
	// index may have characters that require escaping
	reqURL := base + "/" + url.PathEscape(index) + "/_search"

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// auth: prefer explicit params, fallback to env
	if u := getString(params["es_username"]); u != "" {
		req.SetBasicAuth(u, getString(params["es_password"]))
	} else if u := os.Getenv("ES_USERNAME"); u != "" {
		req.SetBasicAuth(u, os.Getenv("ES_PASSWORD"))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elasticsearch error: %s", string(b))
	}

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	// extract hits
	var hits []any
	total := 0
	if h, ok := parsed["hits"].(map[string]any); ok {
		if arr, ok := h["hits"].([]any); ok {
			hits = arr
		}
		if tot, ok := h["total"].(map[string]any); ok {
			if v, ok := tot["value"].(float64); ok {
				total = int(v)
			}
		} else if v, ok := h["total"].(float64); ok {
			total = int(v)
		}
	}

	return map[string]any{
		"took":      parsed["took"],
		"timed_out": parsed["timed_out"],
		"total":     total,
		"hits":      hits,
		"raw":       parsed,
	}, nil
}

// ---------- Splunk (simple REST export) ---------------------------------

func runSplunkQuery(ctx context.Context, params map[string]any, queryStr string) (map[string]any, error) {
	splunkURL := getString(params["splunk_url"])
	if splunkURL == "" {
		splunkURL = os.Getenv("SPLUNK_URL")
		if splunkURL == "" {
			splunkURL = "http://localhost:8089"
		}
	}

	token := getString(params["splunk_token"])
	if token == "" {
		token = os.Getenv("SPLUNK_TOKEN")
	}

	count := getInt(params["count"], 100)

	q := queryStr
	if !strings.HasPrefix(strings.TrimSpace(strings.ToLower(q)), "search") {
		q = "search " + q
	}

	form := url.Values{}
	form.Set("search", q)
	form.Set("output_mode", "json")
	form.Set("count", strconv.Itoa(count))

	exportURL := strings.TrimRight(splunkURL, "/") + "/services/search/jobs/export"
	req, err := http.NewRequestWithContext(ctx, "POST", exportURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if u := os.Getenv("SPLUNK_USER"); u != "" {
		req.SetBasicAuth(u, os.Getenv("SPLUNK_PASSWORD"))
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("splunk error: %s", string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var results []map[string]any
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			// ignore unparsable lines
			continue
		}
		if r, ok := obj["result"]; ok {
			if m, ok := r.(map[string]any); ok {
				results = append(results, m)
				continue
			}
		}
		// if it's not a usual `result` wrapper, append the raw object
		results = append(results, obj)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return map[string]any{"count": len(results), "results": results}, nil
}

// ---------------- helpers -------------------------------------------------

func getString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case float64:
		// JSON numbers decode as float64
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func getInt(v any, def int) int {
	if v == nil {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
		return def
	default:
		return def
	}
}
