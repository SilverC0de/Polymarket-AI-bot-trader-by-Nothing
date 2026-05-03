package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// EventRecorder persists simulation events for GET /finance and GET /finance/history/{page}.
type EventRecorder interface {
	Append(ctx context.Context, kind string, data any) error
	Len(ctx context.Context) (int64, error)
	ListRecent(ctx context.Context, limit int64) ([]PersistedEvent, error)
	ListRange(ctx context.Context, offset, limit int64) ([]PersistedEvent, error)
	Close() error
}

// defaultMaxEvents caps in-memory list length so RSS stays bounded (same order of magnitude as former Redis trim).
const defaultMaxEvents int64 = 50000

// MemoryEventLog keeps a bounded newest-first buffer in memory and logs each append to the given logger (stdout JSON when main uses slog.NewJSONHandler(os.Stdout)).
type MemoryEventLog struct {
	mu     sync.RWMutex
	events []PersistedEvent // index 0 = newest
	maxLen int64
	log    *slog.Logger
}

// NewMemoryEventLog returns an EventRecorder suitable for platforms like Render (audit trail in logs + API pagination from process memory).
func NewMemoryEventLog(logger *slog.Logger) *MemoryEventLog {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryEventLog{
		events: make([]PersistedEvent, 0),
		maxLen: defaultMaxEvents,
		log:    logger,
	}
}

// Append implements EventRecorder.
func (m *MemoryEventLog) Append(ctx context.Context, kind string, data any) error {
	_ = ctx
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	rec := PersistedEvent{TS: time.Now().UTC(), Kind: kind, Data: payload}

	m.log.Info("sim_event",
		slog.String("kind", rec.Kind),
		slog.Time("ts", rec.TS),
		slog.String("data", string(payload)),
	)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append([]PersistedEvent{rec}, m.events...)
	if int64(len(m.events)) > m.maxLen {
		m.events = m.events[:m.maxLen]
	}
	return nil
}

// Len implements EventRecorder.
func (m *MemoryEventLog) Len(ctx context.Context) (int64, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.events)), nil
}

// ListRecent implements EventRecorder (newest first).
func (m *MemoryEventLog) ListRecent(ctx context.Context, limit int64) ([]PersistedEvent, error) {
	_ = ctx
	if limit <= 0 {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := int(limit)
	if n > len(m.events) {
		n = len(m.events)
	}
	out := make([]PersistedEvent, n)
	copy(out, m.events[:n])
	return out, nil
}

// ListRange implements EventRecorder (offset from newest; offset 0 = newest row).
func (m *MemoryEventLog) ListRange(ctx context.Context, offset, limit int64) ([]PersistedEvent, error) {
	_ = ctx
	if limit <= 0 {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	start := int(offset)
	if start < 0 || start >= len(m.events) {
		return nil, nil
	}
	end := start + int(limit)
	if end > len(m.events) {
		end = len(m.events)
	}
	out := make([]PersistedEvent, end-start)
	copy(out, m.events[start:end])
	return out, nil
}

// Close implements EventRecorder.
func (m *MemoryEventLog) Close() error {
	return nil
}
