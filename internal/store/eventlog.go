package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultEventListKey = "pmvibes:sim:eventlog"

// defaultMaxEvents caps Redis list length (LPUSH + LTRIM) so storage stays bounded.
const defaultMaxEvents int64 = 50000

// EventLog appends simulation events to a Redis list (newest-first via LPUSH).
type EventLog struct {
	client *redis.Client
	key    string
	maxLen int64
}

// NewEventLog creates a Redis-backed event log. redisURL empty returns (nil, nil).
func NewEventLog(redisURL string) (*EventLog, error) {
	if redisURL == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return &EventLog{
		client: redis.NewClient(opt),
		key:    defaultEventListKey,
		maxLen: defaultMaxEvents,
	}, nil
}

func (e *EventLog) Close() error {
	if e == nil || e.client == nil {
		return nil
	}
	return e.client.Close()
}

// PersistedEvent is one stored record (trade, skip, outcome, etc.).
type PersistedEvent struct {
	TS   time.Time       `json:"ts"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Append stores an event as JSON (LPUSH + LTRIM).
func (e *EventLog) Append(ctx context.Context, kind string, data any) error {
	if e == nil || e.client == nil {
		return nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	rec := PersistedEvent{TS: time.Now().UTC(), Kind: kind, Data: payload}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	pipe := e.client.Pipeline()
	pipe.LPush(ctx, e.key, string(b))
	pipe.LTrim(ctx, e.key, 0, e.maxLen-1)
	_, err = pipe.Exec(ctx)
	return err
}

// Len returns the number of persisted events.
func (e *EventLog) Len(ctx context.Context) (int64, error) {
	if e == nil || e.client == nil {
		return 0, nil
	}
	return e.client.LLen(ctx, e.key).Result()
}

// ListRecent returns up to limit newest events (same order as LPUSH head: newest first).
func (e *EventLog) ListRecent(ctx context.Context, limit int64) ([]PersistedEvent, error) {
	if e == nil || e.client == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	vals, err := e.client.LRange(ctx, e.key, 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	return parseEvents(vals)
}

// ListRange returns events starting at offset from the newest (offset 0 = newest).
func (e *EventLog) ListRange(ctx context.Context, offset, limit int64) ([]PersistedEvent, error) {
	if e == nil || e.client == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	start := offset
	end := offset + limit - 1
	vals, err := e.client.LRange(ctx, e.key, start, end).Result()
	if err != nil {
		return nil, err
	}
	return parseEvents(vals)
}

func parseEvents(vals []string) ([]PersistedEvent, error) {
	out := make([]PersistedEvent, 0, len(vals))
	for _, v := range vals {
		var ev PersistedEvent
		if json.Unmarshal([]byte(v), &ev) != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}
