// Package db provides the PostgreSQL persistence layer for SOC-OS.
// It uses GORM with the pgx driver so every WorkflowState transition is
// durably recorded as an append-only JSONB document for auditing.
package db

import (
	"fmt"
	"os"

	"github.com/nandu343/real-soc/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store wraps a GORM database connection and exposes typed helpers used by
// Temporal activities and the Fiber API.
type Store struct {
	db *gorm.DB
}

// DSN builds a postgres connection string from environment variables.
// Expected vars: POSTGRES_HOST, POSTGRES_PORT, POSTGRES_USER,
//
//	POSTGRES_PASSWORD, POSTGRES_DB.
//
// Sensible defaults are provided for local docker-compose usage.
func DSN() string {
	host := getenv("POSTGRES_HOST", "localhost")
	port := getenv("POSTGRES_PORT", "5432")
	user := getenv("POSTGRES_USER", "socuser")
	pass := getenv("POSTGRES_PASSWORD", "socpass")
	name := getenv("POSTGRES_DB", "socdb")
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		host, port, user, pass, name,
	)
}

// New opens a GORM connection, runs auto-migrations, and returns a *Store.
// Callers should defer store.Close() when the process exits.
func New(dsn string) (*Store, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("db: open connection: %w", err)
	}

	// Auto-migrate creates / updates the workflow_states table.
	// The JSONB columns (agent_cards, tasks, artifacts) are handled by the
	// custom Scanner/Valuer types defined in models.
	if err := db.AutoMigrate(&models.WorkflowState{}); err != nil {
		return nil, fmt.Errorf("db: auto-migrate: %w", err)
	}

	return &Store{db: db}, nil
}

// Save upserts a WorkflowState record (insert or update on primary key).
// This is called on every state transition so the full history is preserved
// via the updated_at timestamp.
func (s *Store) Save(state *models.WorkflowState) error {
	result := s.db.Save(state)
	if result.Error != nil {
		return fmt.Errorf("db: save state %s: %w", state.TraceID, result.Error)
	}
	return nil
}

// GetByTraceID retrieves the latest WorkflowState for a given trace ID.
func (s *Store) GetByTraceID(traceID string) (*models.WorkflowState, error) {
	var state models.WorkflowState
	result := s.db.First(&state, "trace_id = ?", traceID)
	if result.Error != nil {
		return nil, fmt.Errorf("db: get state %s: %w", traceID, result.Error)
	}
	return &state, nil
}

// ListByWorkflowID returns all states recorded under a Temporal workflow ID,
// ordered by most-recently-updated first.  This gives the full audit trail.
func (s *Store) ListByWorkflowID(workflowID string) ([]models.WorkflowState, error) {
	var states []models.WorkflowState
	result := s.db.
		Where("workflow_id = ?", workflowID).
		Order("updated_at DESC").
		Find(&states)
	if result.Error != nil {
		return nil, fmt.Errorf("db: list states for workflow %s: %w", workflowID, result.Error)
	}
	return states, nil
}

// Close releases the underlying database connection pool.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// getenv returns the value of the named environment variable, or fallback if
// the variable is unset or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
