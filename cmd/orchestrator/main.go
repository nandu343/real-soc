// Command orchestrator is the main entry point for the Real-SOC pipeline server.
//
// It wires together:
//   - A Fiber HTTP server (API + MCP endpoints)
//   - A Temporal worker (executes Activities and Workflows)
//   - A GORM/Postgres store (audit trail)
//   - A Docker runner (agent execution sandbox)
//
// Configuration is entirely via environment variables (see each package for
// the full list).  For local development, use docker-compose.yml to provide
// Temporal and Postgres.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	fblogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/nandu343/real-soc/api"
	"github.com/nandu343/real-soc/internal/db"
	zlog "github.com/nandu343/real-soc/internal/log"
	"github.com/nandu343/real-soc/internal/runner"
	"github.com/nandu343/real-soc/internal/workflow"
	"github.com/nandu343/real-soc/mcp"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// initialize structured JSON logging
	if err := zlog.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer zlog.Sync()

	// ------------------------------------------------------------------
	// Postgres store
	// ------------------------------------------------------------------
	store, err := db.New(db.DSN())
	if err != nil {
		zlog.SugaredWithTrace(context.Background()).Fatalf("failed to connect to postgres: %v", err)
	}
	defer store.Close()
	zlog.SugaredWithTrace(context.Background()).Info("postgres connected")

	// ------------------------------------------------------------------
	// Docker runner
	// ------------------------------------------------------------------
	networkName := getenv("DOCKER_AGENT_NETWORK", "soc-agent-net")
	registryToken := os.Getenv("REGISTRY_TOKEN") // may be empty for public images
	dockerRunner, err := runner.NewDockerRunner(networkName, registryToken)
	if err != nil {
		zlog.SugaredWithTrace(context.Background()).Fatalf("failed to create docker runner: %v", err)
	}
	zlog.SugaredWithTrace(context.Background()).Infow("docker runner ready", "network", networkName)

	// HTTP runner for remote/api agents (optional). Timeout configurable.
	httpTimeout := 30 // seconds
	if v := getenv("HTTP_RUNNER_TIMEOUT", ""); v != "" {
		// attempt to parse but ignore errors and fall back to default
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			httpTimeout = parsed
		}
	}
	httpRunner := runner.NewHTTPRunner(time.Duration(httpTimeout) * time.Second)

	// Optionally start a shared Proxy Agent service so that agents configured
	// with Outbound=="proxy" may reach external services through this
	// controlled gateway. Configure with PROXY_AGENT_IMAGE and
	// PROXY_ALLOWED_HOSTS environment variables.
	proxyImage := os.Getenv("PROXY_AGENT_IMAGE")
	proxyEnabled := false
	proxyURL := ""
	// Pre-compute allowed hosts list so we can include it in the MCP manifest
	proxyAllowed := getenv("PROXY_ALLOWED_HOSTS", "virustotal.com")
	proxyAllowedList := strings.Split(proxyAllowed, ",")
	if proxyImage != "" {
		proxyID, err := dockerRunner.StartProxyService(context.Background(), proxyImage, proxyAllowedList)
		if err != nil {
			zlog.SugaredWithTrace(context.Background()).Fatalf("failed to start proxy agent: %v", err)
		}
		zlog.SugaredWithTrace(context.Background()).Infow("proxy agent started", "container", proxyID)
		proxyEnabled = true
		proxyURL = "http://proxy-agent:8080/forward"
		defer func() {
			if err := dockerRunner.StopProxyService(context.Background()); err != nil {
				zlog.SugaredWithTrace(context.Background()).Warnf("failed to stop proxy agent: %v", err)
			}
		}()
	}

	// ------------------------------------------------------------------
	// Temporal client + worker
	// ------------------------------------------------------------------
	temporalAddress := getenv("TEMPORAL_ADDRESS", "localhost:7233")
	temporalClient, err := client.Dial(client.Options{
		HostPort: temporalAddress,
		Logger:   zlog.NewTemporalLogger(),
	})
	if err != nil {
		zlog.SugaredWithTrace(context.Background()).Fatalf("failed to connect to temporal: %v", err)
	}
	defer temporalClient.Close()
	zlog.SugaredWithTrace(context.Background()).Infow("temporal client connected", "addr", temporalAddress)

	// Register activities and workflows with the worker.
	multiRunner := &runner.MultiRunner{Docker: dockerRunner, HTTP: httpRunner}
	acts := &workflow.Activities{
		Runner: multiRunner,
		Store:  store,
	}

	w := worker.New(temporalClient, workflow.TaskQueueName, worker.Options{})
	w.RegisterWorkflow(workflow.AgentPipelineWorkflow)
	// Register the Interpreter workflow so we can start graph-driven runs.
	w.RegisterWorkflow(workflow.InterpreterWorkflow)
	w.RegisterActivity(acts)

	// Start the worker in a goroutine so it runs alongside the HTTP server.
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			zlog.SugaredWithTrace(context.Background()).Errorf("temporal worker error: %v", err)
		}
	}()
	zlog.SugaredWithTrace(context.Background()).Infow("temporal worker started", "queue", workflow.TaskQueueName)

	// ------------------------------------------------------------------
	// MCP tool registry
	// ------------------------------------------------------------------
	toolRegistry := mcp.NewToolRegistry()
	mcp.RegisterBuiltinTools(toolRegistry)
	mcpServer := mcp.NewMCPServer(toolRegistry)
	// Publish runtime info to the MCP manifest so agents can discover host
	// capabilities (e.g. whether a proxy is available and what its URL is).
	mcpServer.SetManifestExtras(map[string]any{
		"proxy_enabled":       proxyEnabled,
		"proxy_url":           proxyURL,
		"proxy_allowed_hosts": proxyAllowedList,
	})

	// ------------------------------------------------------------------
	// Fiber HTTP server
	// ------------------------------------------------------------------
	app := fiber.New(fiber.Config{
		AppName:      "Real-SOC Orchestrator",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	})

	app.Use(recover.New())
	// Use Fiber's logger middleware but keep the format simple — primary
	// structured logging is handled by zap.
	app.Use(fblogger.New(fblogger.Config{
		Format: "[${time}] ${status} ${method} ${path} ${latency}\n",
	}))

	// Mount REST API routes.
	apiHandler := api.NewHandler(store, temporalClient)
	apiHandler.Register(app)

	// Mount MCP routes.
	mcpServer.Mount(app)

	// ------------------------------------------------------------------
	// Graceful shutdown
	// ------------------------------------------------------------------
	listenAddr := ":" + getenv("PORT", "8080")
	zlog.SugaredWithTrace(context.Background()).Infof("HTTP server listening on %s", listenAddr)

	// Start the server in a goroutine.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- app.Listen(listenAddr)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		zlog.SugaredWithTrace(context.Background()).Info("shutdown signal received")
	case err := <-serverErr:
		zlog.SugaredWithTrace(context.Background()).Errorf("server error: %v", err)
	}

	// Allow in-flight requests up to 10 s to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		zlog.SugaredWithTrace(context.Background()).Errorf("http shutdown error: %v", err)
	}
	w.Stop()
	zlog.SugaredWithTrace(context.Background()).Info("orchestrator stopped")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
