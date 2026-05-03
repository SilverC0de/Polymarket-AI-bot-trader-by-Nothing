package store

import (
	"encoding/json"
	"time"
)

// PersistedEvent is one stored record (trade, skip, outcome, etc.).
type PersistedEvent struct {
	TS   time.Time       `json:"ts"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}
