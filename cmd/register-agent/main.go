package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nandu343/real-soc/internal/models"
)

func main() {
	var id string
	var endpoint string
	var atype string
	var caps string
	var authRaw string

	flag.StringVar(&id, "id", "", "agent id (required)")
	flag.StringVar(&endpoint, "endpoint", "", "agent HTTP endpoint (e.g. https://host:8080/run)")
	flag.StringVar(&atype, "type", "remote", "agent type (remote|api|docker|human)")
	flag.StringVar(&caps, "capabilities", "", "comma-separated capabilities")
	flag.StringVar(&authRaw, "auth", "", "auth JSON for remote agents (e.g. '{\"bearer\":\"TOKEN\"}')")
	var serverURL string
	flag.StringVar(&serverURL, "server", "", "orchestrator base URL (e.g. http://localhost:8080) to register agent remotely")
	flag.Parse()

	if id == "" {
		fmt.Fprintln(os.Stderr, "--id is required")
		flag.Usage()
		os.Exit(2)
	}

	a := models.AgentCard{
		AgentID: id,
		Image:   "",
		Type:    atype,
	}
	if endpoint != "" {
		a.Endpoint = endpoint
	}
	if caps != "" {
		var out []string
		for _, s := range splitAndTrim(caps, ',') {
			out = append(out, s)
		}
		a.Capabilities = out
	}
	if authRaw != "" {
		var auth map[string]any
		if err := json.Unmarshal([]byte(authRaw), &auth); err == nil {
			a.Auth = auth
		}
	}

	// Ensure agents directory
	dir := "agents"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create agents dir: %v\n", err)
		os.Exit(1)
	}

	outPath := filepath.Join(dir, id+".json")
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create file %s: %v\n", outPath, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write agent file: %v\n", err)
		f.Close()
		os.Exit(1)
	}
	f.Close()

	fmt.Printf("wrote %s\n", outPath)

	// If server URL was provided, attempt to POST the agent to the orchestrator
	if serverURL != "" {
		endpoint := strings.TrimRight(serverURL, "/") + "/api/v1/agents"
		body, _ := json.Marshal(a)
		client := &http.Client{Timeout: 5e9} // 5s
		resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to POST to server %s: %v\n", endpoint, err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			var b bytes.Buffer
			b.ReadFrom(resp.Body)
			fmt.Fprintf(os.Stderr, "server returned %d: %s\n", resp.StatusCode, b.String())
			os.Exit(1)
		}
		fmt.Printf("registered agent at %s\n", endpoint)
	}
}

func splitAndTrim(s string, sep rune) []string {
	var out []string
	cur := ""
	for _, ch := range s {
		if ch == sep {
			if t := trimSpace(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if t := trimSpace(cur); t != "" {
		out = append(out, t)
	}
	return out
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
