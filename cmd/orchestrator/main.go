// Command orchestrator is the main entry point for the SOC-OS pipeline server.
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
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/nandu343/real-soc/api"
	"github.com/nandu343/real-soc/internal/db"
	"github.com/nandu343/real-soc/internal/runner"
	"github.com/nandu343/real-soc/internal/workflow"
	"github.com/nandu343/real-soc/mcp"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ------------------------------------------------------------------
	// Postgres store
	// ------------------------------------------------------------------
	store, err := db.New(db.DSN())
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer store.Close()
	log.Println("postgres connected")

	// ------------------------------------------------------------------
	// Docker runner
	// ------------------------------------------------------------------
	networkName := getenv("DOCKER_AGENT_NETWORK", "soc-agent-net")
	registryToken := os.Getenv("REGISTRY_TOKEN") // may be empty for public images
	dockerRunner, err := runner.NewDockerRunner(networkName, registryToken)
	if err != nil {
		log.Fatalf("failed to create docker runner: %v", err)
	}
	log.Println("docker runner ready, network:", networkName)

	// ------------------------------------------------------------------
	// Temporal client + worker
	// ------------------------------------------------------------------
	temporalAddress := getenv("TEMPORAL_ADDRESS", "localhost:7233")
	temporalClient, err := client.Dial(client.Options{
		HostPort: temporalAddress,
	})
	if err != nil {
		log.Fatalf("failed to connect to temporal: %v", err)
	}
	defer temporalClient.Close()
	log.Println("temporal client connected:", temporalAddress)

	// Register activities and workflows with the worker.
	acts := &workflow.Activities{
		Runner: dockerRunner,
		Store:  store,
	}

	w := worker.New(temporalClient, workflow.TaskQueueName, worker.Options{})
	w.RegisterWorkflow(workflow.AgentPipelineWorkflow)
	w.RegisterActivity(acts)

	// Start the worker in a goroutine so it runs alongside the HTTP server.
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			log.Printf("temporal worker error: %v", err)
		}
	}()
	log.Println("temporal worker started, queue:", workflow.TaskQueueName)

	// ------------------------------------------------------------------
	// MCP tool registry
	// ------------------------------------------------------------------
	toolRegistry := mcp.NewToolRegistry()
	mcp.RegisterBuiltinTools(toolRegistry)
	mcpServer := mcp.NewMCPServer(toolRegistry)

	// ------------------------------------------------------------------
	// Fiber HTTP server
	// ------------------------------------------------------------------
	app := fiber.New(fiber.Config{
		AppName:      "SOC-OS Orchestrator",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
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
	log.Printf("HTTP server listening on %s", listenAddr)

	// Start the server in a goroutine.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- app.Listen(listenAddr)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")
	case err := <-serverErr:
		log.Printf("server error: %v", err)
	}

	// Allow in-flight requests up to 10 s to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("http shutdown error: %v", err)
	}
	w.Stop()
	log.Println("orchestrator stopped")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
