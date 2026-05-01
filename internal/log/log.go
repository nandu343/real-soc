package zlog

import (
	"context"

	templog "go.temporal.io/sdk/log"
	"go.uber.org/zap"
)

type ctxKey string

const traceKey ctxKey = "trace_id"

// Logger and Sugar are the process-wide zap instances.
var Logger *zap.Logger
var Sugar *zap.SugaredLogger

// Init initialises a JSON zap logger. Call once at process start.
func Init() error {
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "json"
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.MessageKey = "msg"

	l, err := cfg.Build(zap.AddCaller())
	if err != nil {
		return err
	}
	Logger = l
	Sugar = l.Sugar()
	zap.ReplaceGlobals(Logger)
	zap.RedirectStdLog(Logger)
	return nil
}

// Sync flushes any buffered log entries.
func Sync() error {
	if Logger == nil {
		return nil
	}
	return Logger.Sync()
}

// ContextWithTrace returns a context with the provided trace id value.
func ContextWithTrace(ctx context.Context, trace string) context.Context {
	return context.WithValue(ctx, traceKey, trace)
}

// SugaredWithTrace returns a SugaredLogger augmented with the trace id
// stored in ctx (if any).
func SugaredWithTrace(ctx context.Context) *zap.SugaredLogger {
	if ctx == nil || Sugar == nil {
		return Sugar
	}
	if v := ctx.Value(traceKey); v != nil {
		if s, ok := v.(string); ok {
			return Sugar.With("trace_id", s)
		}
	}
	return Sugar
}

// temporalLogger adapts a zap SugaredLogger to the Temporal log.Logger
// interface so Temporal internals and workflow/activity loggers emit via
// zap in JSON format.
type temporalLogger struct {
	*zap.SugaredLogger
}

func (t *temporalLogger) Debug(msg string, keyvals ...interface{}) {
	t.SugaredLogger.Debugw(msg, keyvals...)
}
func (t *temporalLogger) Info(msg string, keyvals ...interface{}) {
	t.SugaredLogger.Infow(msg, keyvals...)
}
func (t *temporalLogger) Warn(msg string, keyvals ...interface{}) {
	t.SugaredLogger.Warnw(msg, keyvals...)
}
func (t *temporalLogger) Error(msg string, keyvals ...interface{}) {
	t.SugaredLogger.Errorw(msg, keyvals...)
}

// With returns a child logger that prepends the provided keyvals to entries.
func (t *temporalLogger) With(keyvals ...interface{}) templog.Logger {
	return &temporalLogger{t.SugaredLogger.With(keyvals...)}
}

// WithCallerSkip creates a child logger that skips the given number of
// caller frames. This satisfies the optional WithSkipCallers interface.
func (t *temporalLogger) WithCallerSkip(n int) templog.Logger {
	l := t.SugaredLogger.Desugar().WithOptions(zap.AddCallerSkip(n)).Sugar()
	return &temporalLogger{l}
}

// NewTemporalLogger returns a templog.Logger backed by the global Zap logger.
// Call this after Init().
func NewTemporalLogger() templog.Logger {
	if Sugar == nil {
		return &temporalLogger{zap.NewNop().Sugar()}
	}
	return &temporalLogger{Sugar}
}
