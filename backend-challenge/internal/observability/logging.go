package observability

import (
	"io"
	"log/slog"
	"os"
)

// InitLogger configures structured logging. When LOG_JSON=1, outputs JSON for production.
func InitLogger() {
	var handler slog.Handler
	if os.Getenv("LOG_JSON") == "1" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	}
	slog.SetDefault(slog.New(handler))
}

// Logger returns a logger for the given component
func Logger(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

// DiscardLogger returns a logger that writes nowhere (for tests)
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
