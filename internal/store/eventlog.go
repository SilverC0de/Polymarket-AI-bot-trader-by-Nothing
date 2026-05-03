package store

import (
	"fmt"
	"log/slog"
)

// NewEventLog returns Redis-backed storage when redisURL is non-empty, otherwise in-memory.
func NewEventLog(redisURL string, log *slog.Logger) (EventRecorder, error) {
	if redisURL == "" {
		return NewMemoryEventLog(log), nil
	}
	r, err := NewRedisEventLog(redisURL, log)
	if err != nil {
		return nil, fmt.Errorf("redis event log: %w", err)
	}
	return r, nil
}
