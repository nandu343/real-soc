package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ForwardRequest is the request envelope agents will POST to the proxy.
type ForwardRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	TraceID string            `json:"trace_id,omitempty"`
}

// ForwardResponse is returned by the proxy server to callers.
type ForwardResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

func main() {
	allowedEnv := os.Getenv("ALLOWED_HOSTS")
	if allowedEnv == "" {
		log.Println("warning: ALLOWED_HOSTS not set — proxy will reject all requests")
	}
	allowedHosts := parseAllowedHosts(allowedEnv)

	http.HandleFunc("/forward", func(w http.ResponseWriter, r *http.Request) {
		var fr ForwardRequest
		if err := json.NewDecoder(r.Body).Decode(&fr); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		if fr.Method == "" {
			fr.Method = http.MethodGet
		}

		u, err := url.Parse(fr.URL)
		if err != nil || u.Host == "" {
			http.Error(w, "invalid url", http.StatusBadRequest)
			return
		}

		if !hostAllowed(u.Host, allowedHosts) {
			logEvent("blocked", fr.TraceID, u.Host, "host not allowed")
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}

		// Build outbound request
		var body io.Reader
		if fr.Body != "" {
			body = bytes.NewBufferString(fr.Body)
		}
		req, err := http.NewRequest(fr.Method, fr.URL, body)
		if err != nil {
			http.Error(w, "failed to create outbound request", http.StatusInternalServerError)
			return
		}
		for k, v := range fr.Headers {
			req.Header.Set(k, v)
		}
		if fr.TraceID != "" {
			req.Header.Set("X-Trace-ID", fr.TraceID)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logEvent("error", fr.TraceID, u.Host, err.Error())
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		logEvent("forward", fr.TraceID, u.Host, fmt.Sprintf("status=%d size=%d", resp.StatusCode, len(respBody)))

		out := ForwardResponse{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    string(respBody),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("proxy-agent: listening on %s (allowed hosts=%v)\n", addr, allowedHosts)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("proxy-agent: server error: %v", err)
	}
}

func parseAllowedHosts(src string) []string {
	if src == "" {
		return nil
	}
	parts := strings.Split(src, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	// strip optional port
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	for _, a := range allowed {
		if a == host || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func logEvent(action, traceID, host, msg string) {
	entry := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"action":  action,
		"host":    host,
		"message": msg,
	}
	if traceID != "" {
		entry["trace_id"] = traceID
	}
	b, _ := json.Marshal(entry)
	log.Println(string(b))
}
