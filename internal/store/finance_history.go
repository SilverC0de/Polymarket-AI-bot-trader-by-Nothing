package store

import (
	"encoding/json"
)

const (
	eventKindTrade = "trade"
	outcomeWin     = "WIN"
	outcomeLose    = "LOSE"
)

// FilterPersistedForHistory returns events in the same order as all (newest first).
// If learn is true, all is returned unchanged.
// If learn is false, only trade rows resolved to WIN or LOSE are kept (skips, outcomes, and PENDING trades are dropped).
func FilterPersistedForHistory(all []PersistedEvent, learn bool) []PersistedEvent {
	if learn || len(all) == 0 {
		return all
	}
	out := make([]PersistedEvent, 0, len(all))
	for i := range all {
		if isResolvedWinLoseTrade(&all[i]) {
			out = append(out, all[i])
		}
	}
	return out
}

func isResolvedWinLoseTrade(e *PersistedEvent) bool {
	if e == nil || e.Kind != eventKindTrade {
		return false
	}
	var payload struct {
		Outcome string `json:"Outcome"`
	}
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		return false
	}
	switch payload.Outcome {
	case outcomeWin, outcomeLose:
		return true
	default:
		return false
	}
}
