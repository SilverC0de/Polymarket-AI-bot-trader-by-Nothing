package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisEventLog stores simulation events in a Redis list (LPUSH head = newest).
type RedisEventLog struct {
	rdb    *redis.Client
	key    string
	maxLen int64
	log    *slog.Logger
}

// NewRedisEventLog connects and verifies Redis with a short ping timeout.
func NewRedisEventLog(redisURL string, logger *slog.Logger) (*RedisEventLog, error) {
	if logger == nil {
		logger = slog.Default()
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisEventLog{
		rdb:    rdb,
		key:    "pmvibes:sim:events",
		maxLen: defaultMaxEvents,
		log:    logger,
	}, nil
}

// Append implements EventRecorder.
func (r *RedisEventLog) Append(ctx context.Context, kind string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	rec := PersistedEvent{TS: time.Now().UTC(), Kind: kind, Data: payload}

	r.log.Info("sim_event",
		slog.String("kind", rec.Kind),
		slog.Time("ts", rec.TS),
		slog.String("data", string(payload)),
	)

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := r.rdb.LPush(ctx, r.key, line).Err(); err != nil {
		return err
	}
	return r.rdb.LTrim(ctx, r.key, 0, r.maxLen-1).Err()
}

// Len implements EventRecorder.
func (r *RedisEventLog) Len(ctx context.Context) (int64, error) {
	return r.rdb.LLen(ctx, r.key).Result()
}

// ListRecent implements EventRecorder (newest first).
func (r *RedisEventLog) ListRecent(ctx context.Context, limit int64) ([]PersistedEvent, error) {
	if limit <= 0 {
		return nil, nil
	}
	return r.ListRange(ctx, 0, limit)
}

// ListRange implements EventRecorder (offset from newest; offset 0 = newest row).
func (r *RedisEventLog) ListRange(ctx context.Context, offset, limit int64) ([]PersistedEvent, error) {
	if limit <= 0 {
		return nil, nil
	}
	stop := offset + limit - 1
	vals, err := r.rdb.LRange(ctx, r.key, offset, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]PersistedEvent, 0, len(vals))
	for _, v := range vals {
		var rec PersistedEvent
		if err := json.Unmarshal([]byte(v), &rec); err != nil {
			return nil, fmt.Errorf("decode persisted event: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// Close implements EventRecorder.
func (r *RedisEventLog) Close() error {
	return r.rdb.Close()
}
