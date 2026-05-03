package simulator

import (
	"fmt"
	"math"
	"time"
)

// SkipReason explains why a market was skipped.
type SkipReason string

const (
	SkipNone              SkipReason = ""
	SkipPriceDiffTooSmall SkipReason = "price_diff_too_small"    // < MinPriceDiff from target
	SkipPriceDiffTooLarge SkipReason = "price_diff_too_large"    // > MaxPriceDiff from target
	SkipTimingTooEarly    SkipReason = "timing_too_early"        // > 3 min before end
	SkipTimingTooLate     SkipReason = "timing_too_late"         // < 1 min before end
	SkipSidewaysMarket    SkipReason = "sideways_market"         // crossed target both ways
	SkipTrendUnclear      SkipReason = "trend_unclear"           // no clear direction
	SkipSwingDetected     SkipReason = "swing_detected"          // price reversed direction
	SkipNoLiquidity       SkipReason = "no_liquidity"            // can't get good price
	SkipAlreadyResolved   SkipReason = "already_resolved"        // market ended
	SkipWeakMomentum      SkipReason = "weak_momentum"           // price not moving away from target fast enough
	SkipOverextended      SkipReason = "overextended"            // price moved too fast, likely to reverse
)

// Direction represents the predicted market direction.
type Direction string

const (
	DirectionUp   Direction = "UP"
	DirectionDown Direction = "DOWN"
	DirectionNone Direction = "NONE"
)

// PriceSnapshot captures a moment of price data.
type PriceSnapshot struct {
	Timestamp time.Time
	BTCPrice  float64
}

// MarketState tracks the state of a market being watched.
type MarketState struct {
	MarketID       string
	PriceToBeat    float64       // BTC price at market start
	StartTime      time.Time
	EndTime        time.Time
	PriceHistory   []PriceSnapshot
	CrossedAbove   bool          // price went above target
	CrossedBelow   bool          // price went below target
	EnteredTrade   bool
	TradeDirection Direction
	SkipReason     SkipReason
	UpTokenID      string        // Token ID for "Up" outcome (YES)
	DownTokenID    string        // Token ID for "Down" outcome (NO)
}

// StrategyConfig holds the trading strategy parameters.
type StrategyConfig struct {
	MinPriceDiff        float64       // Minimum price difference from target ($40)
	MaxPriceDiff        float64       // Maximum price difference from target ($120)
	MinTimeToEnd        time.Duration // Minimum time before market ends (1 min)
	MaxTimeToEnd        time.Duration // Maximum time before market ends (3 min)
	TradeSize           float64       // Trade size in USD ($5)
	TrendSampleCount    int           // Number of samples to determine trend
	MomentumSamples     int           // Number of recent samples for momentum check
	MinMomentum         float64       // Minimum $/sec momentum away from target
	MaxRecentMove       float64       // Max price move in lookback period before considered overextended
	RecentMoveLookback  time.Duration // How far back to check for rapid moves
}

// DefaultStrategyConfig returns the default configuration based on user requirements.
func DefaultStrategyConfig() StrategyConfig {
	return StrategyConfig{
		MinPriceDiff:        40.0,
		MaxPriceDiff:        120.0,
		MinTimeToEnd:        20 * time.Second,
		MaxTimeToEnd:        2 * time.Minute,
		TradeSize:           5.0,
		TrendSampleCount:    5,
		MomentumSamples:     3,    // Check last 3 samples
		MinMomentum:         0.5,  // Must be moving away at $0.50/sec minimum
		MaxRecentMove:       12.0, // Skip if price moved >$12 in lookback period
		RecentMoveLookback:  60 * time.Second, // Check last 60 seconds
	}
}

// Strategy implements the trading rules.
type Strategy struct {
	config StrategyConfig
}

// NewStrategy creates a new strategy with the given config.
func NewStrategy(config StrategyConfig) *Strategy {
	return &Strategy{config: config}
}

// EvaluateEntry evaluates whether to enter a trade on this market.
func (s *Strategy) EvaluateEntry(state *MarketState, currentPrice float64, now time.Time) (Direction, SkipReason, string) {
	// Check if market already resolved
	if now.After(state.EndTime) || now.Equal(state.EndTime) {
		return DirectionNone, SkipAlreadyResolved, "market has ended"
	}

	// Calculate time to end
	timeToEnd := state.EndTime.Sub(now)

	// Check timing window (1-3 minutes before end)
	if timeToEnd > s.config.MaxTimeToEnd {
		return DirectionNone, SkipTimingTooEarly, fmt.Sprintf("%.0fs to end, need < %.0fs", timeToEnd.Seconds(), s.config.MaxTimeToEnd.Seconds())
	}
	if timeToEnd < s.config.MinTimeToEnd {
		return DirectionNone, SkipTimingTooLate, fmt.Sprintf("%.0fs to end, need > %.0fs", timeToEnd.Seconds(), s.config.MinTimeToEnd.Seconds())
	}

	// Calculate price difference from target
	priceDiff := currentPrice - state.PriceToBeat
	absDiff := math.Abs(priceDiff)

	// Check price difference range (MinPriceDiff–MaxPriceDiff)
	if absDiff < s.config.MinPriceDiff {
		return DirectionNone, SkipPriceDiffTooSmall, fmt.Sprintf("$%.2f diff, need >= $%.2f", absDiff, s.config.MinPriceDiff)
	}
	if absDiff > s.config.MaxPriceDiff {
		return DirectionNone, SkipPriceDiffTooLarge, fmt.Sprintf("$%.2f diff, need <= $%.2f", absDiff, s.config.MaxPriceDiff)
	}

	// Update crossing history
	if currentPrice > state.PriceToBeat {
		state.CrossedAbove = true
	} else if currentPrice < state.PriceToBeat {
		state.CrossedBelow = true
	}

	// Check for sideways market (crossed both directions)
	if state.CrossedAbove && state.CrossedBelow {
		return DirectionNone, SkipSidewaysMarket, "price crossed target both ways"
	}

	// Check for swing using recent price history
	if s.HasSwung(state.PriceHistory, state.PriceToBeat) {
		return DirectionNone, SkipSwingDetected, "price swung back and forth"
	}

	// Determine trend from price history
	trend := s.determineTrend(state.PriceHistory)
	if trend == DirectionNone {
		return DirectionNone, SkipTrendUnclear, "no clear trend direction"
	}

	// Current position relative to target
	currentDirection := DirectionDown
	if currentPrice > state.PriceToBeat {
		currentDirection = DirectionUp
	}

	// Trend must match current position for entry
	if trend != currentDirection {
		return DirectionNone, SkipSwingDetected, fmt.Sprintf("trend is %s but price is %s of target", trend, currentDirection)
	}

	// Check momentum - must be moving away from target at sufficient speed
	momentum := s.calculateMomentum(state.PriceHistory, state.PriceToBeat)
	if momentum < s.config.MinMomentum {
		return DirectionNone, SkipWeakMomentum, fmt.Sprintf("momentum %.2f $/sec, need >= %.2f", momentum, s.config.MinMomentum)
	}

	// Check for overextension - skip if price moved too much recently (likely to reverse)
	recentMove := s.calculateRecentMove(state.PriceHistory, trend)
	if recentMove > s.config.MaxRecentMove {
		return DirectionNone, SkipOverextended, fmt.Sprintf("recent move $%.2f, max $%.2f", recentMove, s.config.MaxRecentMove)
	}

	// All checks passed - enter trade following the trend
	reason := fmt.Sprintf("$%.2f %s target, trend %s, %.0fs to end",
		absDiff,
		map[bool]string{true: "above", false: "below"}[currentPrice > state.PriceToBeat],
		trend,
		timeToEnd.Seconds(),
	)

	return trend, SkipNone, reason
}

// determineTrend analyzes price history to determine the current trend.
func (s *Strategy) determineTrend(history []PriceSnapshot) Direction {
	if len(history) < s.config.TrendSampleCount {
		return DirectionNone
	}

	// Look at the last N samples
	recent := history[len(history)-s.config.TrendSampleCount:]

	// Method 1: Count moves
	upMoves := 0
	downMoves := 0
	for i := 1; i < len(recent); i++ {
		diff := recent[i].BTCPrice - recent[i-1].BTCPrice
		if diff > 0 {
			upMoves++
		} else if diff < 0 {
			downMoves++
		}
	}

	// Method 2: Overall direction from start to end
	overallChange := recent[len(recent)-1].BTCPrice - recent[0].BTCPrice

	// Require both methods to agree
	totalMoves := upMoves + downMoves
	if totalMoves == 0 {
		return DirectionNone
	}

	// 60% threshold for move count
	threshold := float64(totalMoves) * 0.6

	if float64(upMoves) >= threshold && overallChange > 0 {
		return DirectionUp
	}
	if float64(downMoves) >= threshold && overallChange < 0 {
		return DirectionDown
	}

	return DirectionNone
}

// calculateRecentMove returns how much price has moved in the trade direction over the lookback period.
// Used to detect overextended moves that are likely to reverse.
func (s *Strategy) calculateRecentMove(history []PriceSnapshot, direction Direction) float64 {
	if len(history) < 2 {
		return 0
	}

	now := history[len(history)-1].Timestamp
	cutoff := now.Add(-s.config.RecentMoveLookback)

	// Find the oldest sample within the lookback window
	var startPrice float64
	found := false
	for _, snap := range history {
		if snap.Timestamp.After(cutoff) || snap.Timestamp.Equal(cutoff) {
			startPrice = snap.BTCPrice
			found = true
			break
		}
	}

	if !found {
		// All samples are within lookback, use the oldest
		startPrice = history[0].BTCPrice
	}

	endPrice := history[len(history)-1].BTCPrice
	move := endPrice - startPrice

	// Return absolute move in the direction of the trade
	// For UP direction, positive move is "in direction"
	// For DOWN direction, negative move is "in direction"
	if direction == DirectionUp {
		if move > 0 {
			return move
		}
		return 0
	} else if direction == DirectionDown {
		if move < 0 {
			return -move // Return positive value
		}
		return 0
	}
	return 0
}

// calculateMomentum returns the rate of price movement away from target ($/sec).
// Positive = moving away from target, Negative = moving toward target.
func (s *Strategy) calculateMomentum(history []PriceSnapshot, priceToBeat float64) float64 {
	if len(history) < s.config.MomentumSamples {
		return 0
	}

	recent := history[len(history)-s.config.MomentumSamples:]
	first := recent[0]
	last := recent[len(recent)-1]

	// Time difference in seconds
	timeDiff := last.Timestamp.Sub(first.Timestamp).Seconds()
	if timeDiff <= 0 {
		return 0
	}

	// Distance from target at start and end
	startDist := math.Abs(first.BTCPrice - priceToBeat)
	endDist := math.Abs(last.BTCPrice - priceToBeat)

	// Positive momentum = moving away from target
	// Negative momentum = moving toward target
	return (endDist - startDist) / timeDiff
}

// HasSwung checks if the market has swung (reversed direction after establishing a trend).
func (s *Strategy) HasSwung(history []PriceSnapshot, priceToBeat float64) bool {
	if len(history) < 10 {
		return false
	}

	// Check the last 10 samples for reversals
	recent := history[len(history)-10:]

	crossings := 0
	wasAbove := recent[0].BTCPrice > priceToBeat

	for _, snap := range recent[1:] {
		isAbove := snap.BTCPrice > priceToBeat
		if isAbove != wasAbove {
			crossings++
			wasAbove = isAbove
		}
	}

	// More than 1 crossing = swing detected
	return crossings > 1
}

// RecordPrice adds a price snapshot to the market state.
func (s *Strategy) RecordPrice(state *MarketState, price float64, timestamp time.Time) {
	state.PriceHistory = append(state.PriceHistory, PriceSnapshot{
		Timestamp: timestamp,
		BTCPrice:  price,
	})

	// Keep only last 60 samples (1 minute at 1 sample/sec)
	if len(state.PriceHistory) > 60 {
		state.PriceHistory = state.PriceHistory[len(state.PriceHistory)-60:]
	}
}
