package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	maxRedisRequestBytes = 9 * 1024 * 1024
	chunkSizeBytes       = 512 * 1024
	inlinePrefix         = "i:"
	chunkPrefix          = "c:"
	chunkTTL             = 24 * time.Hour
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

	if len(line) <= maxRedisRequestBytes {
		if err := r.rdb.LPush(ctx, r.key, inlinePrefix+string(line)).Err(); err != nil {
			return err
		}
		return r.rdb.LTrim(ctx, r.key, 0, r.maxLen-1).Err()
	}

	chunks := splitBytes(line, chunkSizeBytes)
	if len(chunks) == 0 {
		return fmt.Errorf("split oversized payload into zero chunks")
	}

	eventID := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	for i, chunk := range chunks {
		chunkKey := r.chunkKey(eventID, i)
		if err := r.rdb.Set(ctx, chunkKey, chunk, chunkTTL).Err(); err != nil {
			return err
		}
	}
	if err := r.rdb.LPush(ctx, r.key, fmt.Sprintf("%s%s:%d", chunkPrefix, eventID, len(chunks))).Err(); err != nil {
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
		decoded, err := r.decodeEntry(ctx, v)
		if err != nil {
			return nil, err
		}
		var rec PersistedEvent
		if err := json.Unmarshal(decoded, &rec); err != nil {
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

func (r *RedisEventLog) chunkKey(eventID string, idx int) string {
	return fmt.Sprintf("%s:chunk:%s:%d", r.key, eventID, idx)
}

func splitBytes(data []byte, size int) [][]byte {
	if len(data) == 0 || size <= 0 {
		return nil
	}
	parts := make([][]byte, 0, (len(data)+size-1)/size)
	for start := 0; start < len(data); start += size {
		end := start + size
		if end > len(data) {
			end = len(data)
		}
		parts = append(parts, data[start:end])
	}
	return parts
}

func (r *RedisEventLog) decodeEntry(ctx context.Context, raw string) ([]byte, error) {
	if strings.HasPrefix(raw, inlinePrefix) {
		return []byte(strings.TrimPrefix(raw, inlinePrefix)), nil
	}
	if !strings.HasPrefix(raw, chunkPrefix) {
		return []byte(raw), nil
	}

	meta := strings.TrimPrefix(raw, chunkPrefix)
	parts := strings.Split(meta, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid chunk metadata format")
	}
	eventID := parts[0]
	count, err := strconv.Atoi(parts[1])
	if err != nil || count <= 0 {
		return nil, fmt.Errorf("invalid chunk metadata count")
	}

	var b strings.Builder
	for i := 0; i < count; i++ {
		chunk, err := r.rdb.Get(ctx, r.chunkKey(eventID, i)).Result()
		if err != nil {
			return nil, fmt.Errorf("read chunk %d/%d for %s: %w", i+1, count, eventID, err)
		}
		b.WriteString(chunk)
	}
	return []byte(b.String()), nil
}
