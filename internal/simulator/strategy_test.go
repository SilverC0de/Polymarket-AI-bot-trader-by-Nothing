package simulator

import (
	"testing"
	"time"
)

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

func TestDefaultStrategyRejectsObservedLosingCushions(t *testing.T) {
	strategy := NewStrategy(DefaultStrategyConfig())
	now := time.Unix(0, 0)

	tests := []struct {
		name     string
		price    float64
		wantSkip SkipReason
		wantDir  Direction
	}{
		{name: "up sixty two dollar cushion", price: 10062.60, wantSkip: SkipPriceDiffTooSmall, wantDir: DirectionNone},
		{name: "down forty eight dollar cushion", price: 9951.89, wantSkip: SkipPriceDiffTooSmall, wantDir: DirectionNone},
		{name: "up sixty eight dollar cushion", price: 10068.31, wantSkip: SkipPriceDiffTooSmall, wantDir: DirectionNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &MarketState{
				MarketID:    "test",
				PriceToBeat: 10000,
				StartTime:   now.Add(-3 * time.Minute),
				EndTime:     now.Add(90 * time.Second),
			}

			gotDir, gotSkip, _ := strategy.EvaluateEntry(state, tt.price, now)
			if gotDir != tt.wantDir || gotSkip != tt.wantSkip {
				t.Fatalf("EvaluateEntry() = (%s, %s), want (%s, %s)", gotDir, gotSkip, tt.wantDir, tt.wantSkip)
			}
		})
	}
}

func TestDefaultStrategyAllowsStrongCushionWithTrend(t *testing.T) {
	strategy := NewStrategy(DefaultStrategyConfig())
	now := time.Unix(0, 0)
	state := &MarketState{
		MarketID:    "test",
		PriceToBeat: 10000,
		StartTime:   now.Add(-3 * time.Minute),
		EndTime:     now.Add(90 * time.Second),
		PriceHistory: []PriceSnapshot{
			{Timestamp: now.Add(-4 * time.Second), BTCPrice: 10072},
			{Timestamp: now.Add(-3 * time.Second), BTCPrice: 10072.5},
			{Timestamp: now.Add(-2 * time.Second), BTCPrice: 10073},
			{Timestamp: now.Add(-1 * time.Second), BTCPrice: 10074},
			{Timestamp: now, BTCPrice: 10075},
		},
	}

	gotDir, gotSkip, _ := strategy.EvaluateEntry(state, 10075, now)
	if gotDir != DirectionUp || gotSkip != SkipNone {
		t.Fatalf("EvaluateEntry() = (%s, %s), want (%s, %s)", gotDir, gotSkip, DirectionUp, SkipNone)
	}
}

func TestDangerousPatternTriggersAtSeventyTwoDollarCushion(t *testing.T) {
	strategy := NewStrategy(DefaultStrategyConfig())

	history := []PriceSnapshot{
		{BTCPrice: 76360},
		{BTCPrice: 76420},
		{BTCPrice: 76510},
		{BTCPrice: 76600},
		{BTCPrice: 76530},
		{BTCPrice: 76480},
		{BTCPrice: 76430},
		{BTCPrice: 76400},
		{BTCPrice: 76390},
		{BTCPrice: 76384.5},
	}

	currentPrice := 76384.5
	priceToBeat := 76312.59
	cushion := currentPrice - priceToBeat // ~71.91

	if !strategy.checkDangerousPattern(history, currentPrice, priceToBeat, cushion, DirectionUp) {
		t.Fatalf("checkDangerousPattern() = false, want true for ~72 cushion + weak range position + pullback")
	}
}
