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
	SkipPriceDiffTooSmall SkipReason = "price_diff_too_small" // < MinPriceDiff from target
	SkipPriceDiffTooLarge SkipReason = "price_diff_too_large" // > MaxPriceDiff from target
	SkipTimingTooEarly    SkipReason = "timing_too_early"     // still outside entry window (too long to end)
	SkipTimingTooLate     SkipReason = "timing_too_late"      // past MinTimeToEnd before end
	SkipSidewaysMarket    SkipReason = "sideways_market"      // crossed target both ways
	SkipTrendUnclear      SkipReason = "trend_unclear"        // no clear direction
	SkipSwingDetected     SkipReason = "swing_detected"       // price reversed direction
	SkipNoLiquidity       SkipReason = "no_liquidity"         // can't get good price
	SkipAlreadyResolved   SkipReason = "already_resolved"     // market ended
	SkipWeakMomentum      SkipReason = "weak_momentum"        // price not moving away from target fast enough
	SkipOverextended      SkipReason = "overextended"         // price moved too fast, likely to reverse
	SkipDangerousPattern  SkipReason = "dangerous_pattern"    // combination of small cushion, bad position, and pullback
	SkipApproachingTarget SkipReason = "approaching_target"   // price retreated toward target from its 60s extreme
	SkipNetAdverseMove    SkipReason = "net_adverse_move"     // price net moved against the trade direction over the lookback window
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
	PriceToBeat    float64 // BTC price at market start
	StartTime      time.Time
	EndTime        time.Time
	PriceHistory   []PriceSnapshot
	CrossedAbove   bool // price went above target
	CrossedBelow   bool // price went below target
	EnteredTrade   bool
	TradeDirection Direction
	SkipReason     SkipReason
	UpTokenID      string // Token ID for "Up" outcome (YES)
	DownTokenID    string // Token ID for "Down" outcome (NO)
	// Experimental strategy state (last-30s spike entry).
	ExperimentalArmed         bool
	ExperimentalDirection     Direction
	ExperimentalBaseCoinbase  float64
	ExperimentalLastOBCheck   time.Time
	ExperimentalLastFillPrice float64
	ExperimentalAvgPriceOK    bool
	ExperimentalTrigger1Armed bool
	ExperimentalTrigger2Armed bool
}

// ExperimentalConfig holds late-window spike-entry parameters.
type ExperimentalConfig struct {
	Window             time.Duration             // Late entry window before market end
	TradeSize          float64                   // Trade size in USD
	OrderbookCheckFreq time.Duration             // How often to refresh order book qualification
	TriggerA           ExperimentalTriggerConfig // Trigger 1: dual-feed + avg + spike
	TriggerB           ExperimentalTriggerConfig // Trigger 2: stronger dual-feed + avg only
}

// ExperimentalTriggerConfig is one independent experimental entry trigger.
type ExperimentalTriggerConfig struct {
	DualFeedDiff       float64 // Min absolute diff from target on both feeds
	StartingMinAvgFill float64 // Starting minimum average fill requirement
	LowestMinAvgFill   float64 // Lowest allowed minimum average fill after dynamic relaxation
	RequireSpike       bool    // Whether a spike gate must be met
	SpikeMinThreshold  float64 // Spike threshold floor (e.g., 3)
	SpikeVolMultiplier float64 // Dynamic spike threshold: k * recent_5s_vol
}

// StrategyConfig holds the trading strategy parameters.
type StrategyConfig struct {
	MinPriceDiff       float64       // Minimum price difference from target ($70)
	MaxPriceDiff       float64       // Maximum price difference from target ($120)
	MinTimeToEnd       time.Duration // Minimum time before market ends (30s)
	MaxTimeToEnd       time.Duration // Maximum time before market ends (3 min)
	TradeSize          float64       // Trade size in USD ($10)
	TrendSampleCount   int           // Number of samples to determine trend
	MomentumSamples    int           // Number of recent samples for momentum check
	MinMomentum        float64       // Minimum $/sec momentum away from target (for small cushions)
	MaxRecentMove      float64       // Absolute max price move before considered overextended
	MaxRecentMoveRatio float64       // Max recent move as ratio of cushion (e.g., 0.6 = 60%)
	RecentMoveLookback time.Duration // How far back to check for rapid moves
	MaxEntryPrice      float64       // Max average fill price for order book check
	MinProfitUSD       float64       // Minimum profit for order book check
	// Cushion-based scaling
	LargeCushionThreshold float64 // Cushion above this gets relaxed momentum check ($60)
	LargeCushionMomentum  float64 // Minimum momentum for large cushions ($/sec)
	// Dangerous pattern filter (all 3 must be true to reject)
	DangerCushionLimit    float64 // Max cushion to be considered "small" ($60)
	DangerPositionLimit   float64 // Min position in range to avoid being "bad" (0.35 = 35%)
	DangerPullbackPercent float64 // Max pullback as % of cushion (45%)
	// Approach-from-extreme filter
	MaxExtremeApproach float64 // Max $ the price may have retreated toward target from its 60s extreme
	// Net adverse move filter
	MinAdverseNetMove float64 // Skip if price net moved this many $ against the trade direction over the lookback window
	// Experimental strategy parameters
	Experimental ExperimentalConfig
}

// DefaultStrategyConfig returns the default configuration based on user requirements.
func DefaultStrategyConfig() StrategyConfig {
	return StrategyConfig{
		MinPriceDiff:       70.0,
		MaxPriceDiff:       120.0,
		MinTimeToEnd:       30 * time.Second,
		MaxTimeToEnd:       2 * time.Minute,
		TradeSize:          10.0,
		TrendSampleCount:   5,
		MomentumSamples:    3,                // Check last 3 samples
		MinMomentum:        0.5,              // Must be moving away at $0.50/sec minimum (for small cushions)
		MaxRecentMove:      50.0,             // Absolute cap: skip if moved >$50 regardless of cushion
		MaxRecentMoveRatio: 0.70,             // Skip if recent move > 70% of cushion
		RecentMoveLookback: 60 * time.Second, // Check last 60 seconds
		MaxEntryPrice:      0.995,            // Order book check - slightly more lenient
		MinProfitUSD:       0.10,             // Order book check - very lenient
		// Cushion-based scaling for momentum (threshold must be <= MinPriceDiff to be active)
		LargeCushionThreshold: 70.0, // Cushions >= $70 get relaxed momentum check
		LargeCushionMomentum:  0.15, // Only need $0.15/sec momentum with large cushion
		// Dangerous pattern filter (all 3 must be true to reject)
		DangerCushionLimit:    80.0, // Cushions below $80 are treated as potentially risky
		DangerPositionLimit:   0.35, // Position must be < 35% to be risky
		DangerPullbackPercent: 45.0, // Pullback must be > 45% of cushion to be risky
		// Approach-from-extreme filter
		MaxExtremeApproach: 20.0, // Skip if price retreated >$20 toward target from its 60s extreme
		// Net adverse move filter
		MinAdverseNetMove: 5.0, // Skip if price net moved >$5 against the trade direction over 60s
		Experimental: ExperimentalConfig{
			Window:             30 * time.Second,
			TradeSize:          2.0,
			OrderbookCheckFreq: 1 * time.Second,
			TriggerA: ExperimentalTriggerConfig{
				DualFeedDiff:       40.0,
				StartingMinAvgFill: 0.96,
				LowestMinAvgFill:   0.94,
				RequireSpike:       true,
				SpikeMinThreshold:  4.0,
				SpikeVolMultiplier: 1.0,
			},
			TriggerB: ExperimentalTriggerConfig{
				DualFeedDiff:       70.0,
				StartingMinAvgFill: 0.98,
				LowestMinAvgFill:   0.96,
				RequireSpike:       false,
			},
		},
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

	// Check timing window (MinTimeToEnd–MaxTimeToEnd before end)
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

	// Check momentum - requirement scales with cushion size
	// Large cushions need less momentum (the cushion itself provides safety)
	momentum := s.calculateMomentum(state.PriceHistory, state.PriceToBeat)
	requiredMomentum := s.config.MinMomentum
	if absDiff >= s.config.LargeCushionThreshold {
		// Large cushion - relax momentum requirement
		requiredMomentum = s.config.LargeCushionMomentum
	}
	if momentum < requiredMomentum {
		return DirectionNone, SkipWeakMomentum, fmt.Sprintf("momentum %.2f $/sec, need >= %.2f (cushion $%.0f)", momentum, requiredMomentum, absDiff)
	}

	// Check for overextension - combines absolute limit AND cushion-relative limit
	// Skip only if recent move exceeds BOTH:
	// 1. Absolute cap (MaxRecentMove) - catches extreme moves
	// 2. Cushion ratio (MaxRecentMoveRatio) - ensures enough buffer remains
	recentMove := s.calculateRecentMove(state.PriceHistory, trend)
	maxAllowedByRatio := absDiff * s.config.MaxRecentMoveRatio
	remainingCushion := absDiff - recentMove

	// Only skip if BOTH conditions are violated (recent move is dangerous by both measures)
	if recentMove > s.config.MaxRecentMove && recentMove > maxAllowedByRatio {
		return DirectionNone, SkipOverextended, fmt.Sprintf("recent move $%.2f exceeds both abs cap $%.2f and %.0f%% of cushion ($%.2f)",
			recentMove, s.config.MaxRecentMove, s.config.MaxRecentMoveRatio*100, maxAllowedByRatio)
	}

	// Also skip if remaining cushion after the move is too small (less than MinPriceDiff)
	if remainingCushion < s.config.MinPriceDiff {
		return DirectionNone, SkipOverextended, fmt.Sprintf("remaining cushion $%.2f after $%.2f move, need >= $%.2f",
			remainingCushion, recentMove, s.config.MinPriceDiff)
	}

	// Check for dangerous entry pattern - only reject if ALL three conditions are met
	if hasDangerousPattern := s.checkDangerousPattern(state.PriceHistory, currentPrice, state.PriceToBeat, absDiff, trend); hasDangerousPattern {
		return DirectionNone, SkipDangerousPattern, "small cushion + bad entry position + significant pullback"
	}

	// Check approach from extreme — skip if price has retreated too far toward target
	// from its most distant point in the 60s window. Catches slow sustained approaches
	// (Trade #37: $83 retreat) and sharp bounce reversals (Trade #73: $22 retreat).
	extremeApproach := s.calculateApproachFromExtreme(state.PriceHistory, state.PriceToBeat, absDiff)
	if extremeApproach > s.config.MaxExtremeApproach {
		return DirectionNone, SkipApproachingTarget, fmt.Sprintf(
			"price retreated $%.2f toward target from its 60s extreme (max $%.0f allowed)",
			extremeApproach, s.config.MaxExtremeApproach,
		)
	}

	// Check net adverse move — skip if the price net moved against the trade direction
	// over the lookback window. Catches flat/drifting markets where the micro-trend
	// points the right way but the broader 60s move is against the bet (Trade #100: +$17 UP on a DOWN trade).
	adverseMove := s.calculateAdverseNetMove(state.PriceHistory, trend)
	if adverseMove > s.config.MinAdverseNetMove {
		return DirectionNone, SkipNetAdverseMove, fmt.Sprintf(
			"price net moved $%.2f against %s trade over lookback (max $%.0f allowed)",
			adverseMove, trend, s.config.MinAdverseNetMove,
		)
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

// checkDangerousPattern checks for the specific dangerous entry pattern.
// Returns true only if ALL three conditions are met:
// 1. Small-medium cushion (< DangerCushionLimit)
// 2. Poor entry position (< DangerPositionLimit of 60s range)
// 3. Significant pullback (> DangerPullbackPercent of cushion)
func (s *Strategy) checkDangerousPattern(history []PriceSnapshot, currentPrice, priceToBeat, cushion float64, direction Direction) bool {
	if len(history) < 10 {
		return false // Not enough data to evaluate
	}

	// Check condition 1: Small cushion
	if cushion >= s.config.DangerCushionLimit {
		return false // Large cushion, not dangerous
	}

	// Calculate 60s price range
	prices := make([]float64, len(history))
	for i, snap := range history {
		prices[i] = snap.BTCPrice
	}
	minPrice := prices[0]
	maxPrice := prices[0]
	for _, p := range prices {
		if p < minPrice {
			minPrice = p
		}
		if p > maxPrice {
			maxPrice = p
		}
	}

	priceRange := maxPrice - minPrice
	if priceRange == 0 {
		return false // No range to evaluate
	}

	// Calculate entry position in range
	var positionInRange float64
	var pullbackFromExtreme float64

	if direction == DirectionUp {
		positionInRange = (currentPrice - minPrice) / priceRange
		pullbackFromExtreme = maxPrice - currentPrice
	} else { // DirectionDown
		positionInRange = (maxPrice - currentPrice) / priceRange
		pullbackFromExtreme = currentPrice - minPrice
	}

	// Check condition 2: Bad entry position
	if positionInRange >= s.config.DangerPositionLimit {
		return false // Good position, not dangerous
	}

	// Check condition 3: Significant pullback
	pullbackPercent := (pullbackFromExtreme / cushion) * 100
	if pullbackPercent <= s.config.DangerPullbackPercent {
		return false // Acceptable pullback, not dangerous
	}

	// All three conditions met - this is a dangerous pattern
	return true
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

// calculateApproachFromExtreme returns how far the price has retreated toward the
// target from its most distant (safest) point within the lookback window.
// A large value means the price was safely far from the target but has since
// come back toward it — a reversal signal regardless of the net direction.
func (s *Strategy) calculateApproachFromExtreme(history []PriceSnapshot, priceToBeat, currentDist float64) float64 {
	if len(history) < 2 {
		return 0
	}
	maxDist := currentDist
	for _, snap := range history {
		if d := math.Abs(snap.BTCPrice - priceToBeat); d > maxDist {
			maxDist = d
		}
	}
	return maxDist - currentDist
}

// calculateAdverseNetMove returns how far the price net moved AGAINST the trade
// direction over the lookback window (start-to-end). A positive return means the
// price drifted the wrong way; zero means it moved in the trade direction or was flat.
func (s *Strategy) calculateAdverseNetMove(history []PriceSnapshot, direction Direction) float64 {
	if len(history) < 2 {
		return 0
	}

	now := history[len(history)-1].Timestamp
	cutoff := now.Add(-s.config.RecentMoveLookback)

	startPrice := history[0].BTCPrice
	for _, snap := range history {
		if snap.Timestamp.After(cutoff) || snap.Timestamp.Equal(cutoff) {
			startPrice = snap.BTCPrice
			break
		}
	}

	endPrice := history[len(history)-1].BTCPrice
	netMove := endPrice - startPrice // positive = price went UP

	switch direction {
	case DirectionDown:
		// For DOWN trade, UP movement is adverse
		if netMove > 0 {
			return netMove
		}
	case DirectionUp:
		// For UP trade, DOWN movement is adverse
		if netMove < 0 {
			return -netMove
		}
	}
	return 0
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
