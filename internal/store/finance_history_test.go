package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silver/pmvibes/internal/store"
)

func TestFilterPersistedForHistory(t *testing.T) {
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tradePending := mustJSON(t, map[string]any{"Outcome": "PENDING", "ID": 1})
	tradeWin := mustJSON(t, map[string]any{"Outcome": "WIN", "ID": 2})
	tradeLose := mustJSON(t, map[string]any{"Outcome": "LOSE", "ID": 3})
	skip := json.RawMessage(`{"Reason":"timing"}`)
	outcome := json.RawMessage(`{"MarketID":"x"}`)

	all := []store.PersistedEvent{
		{TS: ts, Kind: "skip", Data: skip},
		{TS: ts, Kind: "trade", Data: tradeWin},
		{TS: ts, Kind: "outcome", Data: outcome},
		{TS: ts, Kind: "trade", Data: tradePending},
		{TS: ts, Kind: "trade", Data: tradeLose},
	}

	filtered := store.FilterPersistedForHistory(all, false)
	if len(filtered) != 2 {
		t.Fatalf("len filtered = %d, want 2 (WIN and LOSE only)", len(filtered))
	}
	if filtered[0].Kind != "trade" || string(filtered[0].Data) != string(tradeWin) {
		t.Fatalf("first event: %+v", filtered[0])
	}
	if filtered[1].Kind != "trade" || string(filtered[1].Data) != string(tradeLose) {
		t.Fatalf("second event: %+v", filtered[1])
	}

	full := store.FilterPersistedForHistory(all, true)
	if len(full) != len(all) {
		t.Fatalf("learn=true: len %d, want %d", len(full), len(all))
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
