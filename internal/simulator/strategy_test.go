package simulator

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateEntrySkipsPullbackTowardTarget(t *testing.T) {
	strategy := NewStrategy(DefaultStrategyConfig())
	now := time.Date(2026, 5, 4, 7, 48, 14, 0, time.UTC)
	state := &MarketState{
		MarketID:    "2146552",
		PriceToBeat: 79829.86,
		StartTime:   now.Add(-3 * time.Minute),
		EndTime:     now.Add(90 * time.Second),
	}

	for i, price := range []float64{
		79894.60,
		79860.00,
		79863.00,
		79866.00,
		79868.00,
		79870.25,
	} {
		state.PriceHistory = append(state.PriceHistory, PriceSnapshot{
			Timestamp: now.Add(time.Duration(i-5) * time.Second),
			BTCPrice:  price,
		})
	}

	direction, skipReason, reason := strategy.EvaluateEntry(state, 79870.25, now)
	if direction != DirectionNone {
		t.Fatalf("direction = %s, want NONE", direction)
	}
	if skipReason != SkipSwingDetected {
		t.Fatalf("skipReason = %s, want %s", skipReason, SkipSwingDetected)
	}
	if !strings.Contains(reason, "pullback toward target") {
		t.Fatalf("reason = %q, want pullback detail", reason)
	}
}

func TestPotentialProfit(t *testing.T) {
	got := potentialProfit(20, 0.99)
	if got < 0.20 || got > 0.21 {
		t.Fatalf("potentialProfit(20, 0.99) = %.4f, want about 0.20", got)
	}

	got = potentialProfit(20, 0.85)
	if got < 3.52 || got > 3.54 {
		t.Fatalf("potentialProfit(20, 0.85) = %.4f, want about 3.53", got)
	}
}
