package simulator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/silver/pmvibes/pkg/polymarket"
)

// TradeOutcome represents the result of a trade.
type TradeOutcome string

const (
	OutcomePending TradeOutcome = "PENDING"
	OutcomeWin     TradeOutcome = "WIN"
	OutcomeLose    TradeOutcome = "LOSE"
)

// TradeDebugContext contains diagnostic info for analyzing losing trades.
type TradeDebugContext struct {
	PriceHistory   []PriceSnapshot `json:"price_history"`    // Price samples from entry to resolution
	EntryMomentum  float64         `json:"entry_momentum"`   // Momentum at entry ($/sec)
	EntryRecentMove float64        `json:"entry_recent_move"` // How much price moved before entry ($)
}

// SimulatedTrade represents a simulated trade entry.
type SimulatedTrade struct {
	ID             int
	MarketID       string
	EntryTime      time.Time
	MarketEndTime  time.Time
	Direction      Direction // UP or DOWN
	PriceToBeat    float64   // BTC price at market start
	EntryBTCPrice  float64   // BTC price when trade was entered
	EntryReason    string
	TradeSize      float64      // USD amount ($5)
	EntryPrice     float64      // Price paid for the outcome token (e.g., 0.55)
	RealOrderBook  bool         // True if EntryPrice came from real order book, false if simulated
	Outcome        TradeOutcome // WIN, LOSE, or PENDING
	FinalBTCPrice  float64      // BTC price at market end
	PnL            float64      // Profit/Loss in USD
	DebugContext   *TradeDebugContext `json:"debug_context,omitempty"` // Only populated for losing trades
}

// SkippedMarket records a market that was skipped with reason.
type SkippedMarket struct {
	MarketID     string
	Timestamp    time.Time
	Reason       SkipReason
	Details      string
	BTCPrice     float64
	PriceToBeat  float64
	TimeToEnd    time.Duration
	PriceHistory []PriceSnapshot `json:"price_history,omitempty"`
}

// SimulationStats holds aggregate statistics.
type SimulationStats struct {
	TotalMarketsObserved int
	TotalTradesEntered   int
	TotalMarketsSkipped  int
	TotalWins            int
	TotalLosses          int
	TotalPending         int
	TotalPnL             float64
	WinRate              float64
	SkipReasons          map[SkipReason]int
}

// MarketOutcome records the result of a 5-minute market.
type MarketOutcome struct {
	MarketID      string
	EndTime       time.Time
	PriceToBeat   float64
	FinalPrice    float64
	Result        Direction // UP or DOWN
	PriceDiff     float64
	WeTradedIt    bool
	OurDirection  Direction
	OurPnL        float64
}

// Engine runs the trading simulation.
type Engine struct {
	mu             sync.RWMutex
	strategy       *Strategy
	pmClient       *polymarket.Client // Polymarket client for fetching real order book prices
	trades         []SimulatedTrade
	skippedMarkets []SkippedMarket
	marketStates   map[string]*MarketState
	marketOutcomes []MarketOutcome
	tradeCounter   int
	startTime      time.Time
	onTradeUpdate  func(trade SimulatedTrade)
	onSkip         func(skip SkippedMarket)
	onMarketEnd    func(outcome MarketOutcome)
}

// NewEngine creates a new simulation engine.
// Pass a polymarket client to use real order book prices, or nil for simulated prices.
func NewEngine(strategy *Strategy, pmClient *polymarket.Client) *Engine {
	return &Engine{
		strategy:       strategy,
		pmClient:       pmClient,
		trades:         make([]SimulatedTrade, 0),
		skippedMarkets: make([]SkippedMarket, 0),
		marketStates:   make(map[string]*MarketState),
		marketOutcomes: make([]MarketOutcome, 0),
		startTime:      time.Now(),
	}
}

// SetTradeCallback sets a callback for trade updates.
func (e *Engine) SetTradeCallback(fn func(trade SimulatedTrade)) {
	e.onTradeUpdate = fn
}

// SetSkipCallback sets a callback for skipped markets.
func (e *Engine) SetSkipCallback(fn func(skip SkippedMarket)) {
	e.onSkip = fn
}

// SetMarketEndCallback sets a callback for when markets resolve.
func (e *Engine) SetMarketEndCallback(fn func(outcome MarketOutcome)) {
	e.onMarketEnd = fn
}

// GetOrCreateMarketState gets or creates a market state.
func (e *Engine) GetOrCreateMarketState(marketID string, priceToBeat float64, startTime, endTime time.Time) *MarketState {
	e.mu.Lock()
	defer e.mu.Unlock()

	if state, exists := e.marketStates[marketID]; exists {
		return state
	}

	state := &MarketState{
		MarketID:     marketID,
		PriceToBeat:  priceToBeat,
		StartTime:    startTime,
		EndTime:      endTime,
		PriceHistory: make([]PriceSnapshot, 0),
	}
	e.marketStates[marketID] = state
	return state
}

// SetMarketTokenIDs sets the token IDs for a market's UP/DOWN outcomes.
// Call this after discovering the market to enable real order book pricing.
func (e *Engine) SetMarketTokenIDs(marketID, upTokenID, downTokenID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if state, exists := e.marketStates[marketID]; exists {
		state.UpTokenID = upTokenID
		state.DownTokenID = downTokenID
	}
}

// getRealEntryPrice fetches the actual fill price from Polymarket order book.
// Returns the price and true if successful, or 0 and false if unavailable.
func (e *Engine) getRealEntryPrice(ctx context.Context, state *MarketState, direction Direction, tradeSize float64) (float64, bool) {
	if e.pmClient == nil {
		return 0, false
	}

	// Determine which token to buy based on direction
	var tokenID string
	if direction == DirectionUp {
		tokenID = state.UpTokenID
	} else {
		tokenID = state.DownTokenID
	}

	if tokenID == "" {
		return 0, false
	}

	// Fetch order book
	ob, err := e.pmClient.GetOrderBook(ctx, tokenID)
	if err != nil {
		return 0, false
	}

	// Get fill price (we're buying, so we hit asks)
	price, filled := ob.GetFillPrice("buy", tradeSize)
	if !filled {
		// Try best price if we couldn't fill entirely
		bestPrice, ok := ob.GetBestPrice("buy")
		if ok {
			return bestPrice, true
		}
		return 0, false
	}

	return price, true
}

// ProcessPriceUpdate processes a new BTC price update for all active markets.
func (e *Engine) ProcessPriceUpdate(btcPrice float64, timestamp time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for marketID, state := range e.marketStates {
		// Skip if already traded or skipped
		if state.EnteredTrade || state.SkipReason != SkipNone {
			continue
		}

		// Record price
		e.strategy.RecordPrice(state, btcPrice, timestamp)

		// Evaluate entry
		direction, skipReason, reason := e.strategy.EvaluateEntry(state, btcPrice, timestamp)

		if skipReason != SkipNone {
			// Don't permanently skip for conditions that can change:
			// - Timing too early (will enter window later)
			// - Price diff too small/large (BTC price fluctuates)
			// - Trend unclear (trend can become clear)
			// - Swing detected (price can stabilize)
			if skipReason == SkipTimingTooEarly ||
				skipReason == SkipPriceDiffTooSmall ||
				skipReason == SkipPriceDiffTooLarge ||
				skipReason == SkipTrendUnclear ||
				skipReason == SkipSwingDetected {
				continue
			}

			// Permanent skip (sideways market, timing too late, etc.)
			state.SkipReason = skipReason
			skip := SkippedMarket{
				MarketID:     marketID,
				Timestamp:    timestamp,
				Reason:       skipReason,
				Details:      reason,
				BTCPrice:     btcPrice,
				PriceToBeat:  state.PriceToBeat,
				TimeToEnd:    state.EndTime.Sub(timestamp),
				PriceHistory: state.PriceHistory,
			}
			e.skippedMarkets = append(e.skippedMarkets, skip)

			if e.onSkip != nil {
				go e.onSkip(skip)
			}
			continue
		}

		if direction != DirectionNone {
			// Enter trade
			e.tradeCounter++
			state.EnteredTrade = true
			state.TradeDirection = direction

			// Try to get real entry price from order book
			var entryPrice float64
			var realOrderBook bool
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if realPrice, ok := e.getRealEntryPrice(ctx, state, direction, e.strategy.config.TradeSize); ok {
				entryPrice = realPrice
				realOrderBook = true
			} else {
				// Fall back to simulated price
				entryPrice = e.calculateEntryPrice(btcPrice, state.PriceToBeat, direction)
				realOrderBook = false
			}
			cancel()

			// Capture debug metrics at entry
			entryMomentum := e.strategy.calculateMomentum(state.PriceHistory, state.PriceToBeat)
			entryRecentMove := e.strategy.calculateRecentMove(state.PriceHistory, direction)

			trade := SimulatedTrade{
				ID:            e.tradeCounter,
				MarketID:      marketID,
				EntryTime:     timestamp,
				MarketEndTime: state.EndTime,
				Direction:     direction,
				PriceToBeat:   state.PriceToBeat,
				EntryBTCPrice: btcPrice,
				EntryReason:   reason,
				TradeSize:     e.strategy.config.TradeSize,
				EntryPrice:    entryPrice,
				RealOrderBook: realOrderBook,
				Outcome:       OutcomePending,
				DebugContext: &TradeDebugContext{
					EntryMomentum:   entryMomentum,
					EntryRecentMove: entryRecentMove,
					PriceHistory:    make([]PriceSnapshot, len(state.PriceHistory)),
				},
			}
			// Copy price history at entry
			copy(trade.DebugContext.PriceHistory, state.PriceHistory)

			e.trades = append(e.trades, trade)

			if e.onTradeUpdate != nil {
				go e.onTradeUpdate(trade)
			}
		}
	}
}

// calculateEntryPrice simulates the entry price for a trade.
func (e *Engine) calculateEntryPrice(btcPrice, priceToBeat float64, direction Direction) float64 {
	diff := btcPrice - priceToBeat
	absDiff := diff
	if absDiff < 0 {
		absDiff = -absDiff
	}

	// Map $30-60 diff to 0.50-0.65 entry price
	// Higher diff = more confident = pay more
	ratio := (absDiff - 30) / 30 // 0 to 1
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}

	// Entry price between 0.50 and 0.65
	return 0.50 + (ratio * 0.15)
}

// ResolveMarket resolves a market with the final BTC price.
func (e *Engine) ResolveMarket(marketID string, finalBTCPrice float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state, exists := e.marketStates[marketID]
	if !exists {
		return
	}

	// Determine winning direction
	var winningDirection Direction
	if finalBTCPrice >= state.PriceToBeat {
		winningDirection = DirectionUp
	} else {
		winningDirection = DirectionDown
	}

	// Create market outcome record
	outcome := MarketOutcome{
		MarketID:    marketID,
		EndTime:     state.EndTime,
		PriceToBeat: state.PriceToBeat,
		FinalPrice:  finalBTCPrice,
		Result:      winningDirection,
		PriceDiff:   finalBTCPrice - state.PriceToBeat,
		WeTradedIt:  false,
	}

	// Update trade if we entered this market
	for i := range e.trades {
		if e.trades[i].MarketID == marketID && e.trades[i].Outcome == OutcomePending {
			e.trades[i].FinalBTCPrice = finalBTCPrice
			outcome.WeTradedIt = true
			outcome.OurDirection = e.trades[i].Direction

			if e.trades[i].Direction == winningDirection {
				e.trades[i].Outcome = OutcomeWin
				// Win payout: (1 - entry_price) * shares
				shares := e.trades[i].TradeSize / e.trades[i].EntryPrice
				e.trades[i].PnL = (1 - e.trades[i].EntryPrice) * shares
				// Clear debug context for wins (not needed)
				e.trades[i].DebugContext = nil
			} else {
				e.trades[i].Outcome = OutcomeLose
				// Lose: lose entire stake
				e.trades[i].PnL = -e.trades[i].TradeSize
				// Append post-entry price history for debugging
				if e.trades[i].DebugContext != nil {
					e.trades[i].DebugContext.PriceHistory = append(
						e.trades[i].DebugContext.PriceHistory,
						state.PriceHistory...,
					)
				}
			}

			outcome.OurPnL = e.trades[i].PnL

			if e.onTradeUpdate != nil {
				go e.onTradeUpdate(e.trades[i])
			}
		}
	}

	// Record outcome
	e.marketOutcomes = append(e.marketOutcomes, outcome)

	// Notify about market end
	if e.onMarketEnd != nil {
		go e.onMarketEnd(outcome)
	}

	// Clean up market state
	delete(e.marketStates, marketID)
}

// GetMarketOutcomes returns all market outcomes.
func (e *Engine) GetMarketOutcomes() []MarketOutcome {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]MarketOutcome, len(e.marketOutcomes))
	copy(result, e.marketOutcomes)
	return result
}

// ActiveMarketInfo contains info about an active market for display.
type ActiveMarketInfo struct {
	MarketID    string
	PriceToBeat float64
	EndTime     time.Time
	TimeToEnd   time.Duration
}

// GetActiveMarkets returns info about currently tracked markets.
func (e *Engine) GetActiveMarkets() []ActiveMarketInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now()
	var result []ActiveMarketInfo
	for _, state := range e.marketStates {
		if state.SkipReason == "" && !state.EnteredTrade {
			result = append(result, ActiveMarketInfo{
				MarketID:    state.MarketID,
				PriceToBeat: state.PriceToBeat,
				EndTime:     state.EndTime,
				TimeToEnd:   state.EndTime.Sub(now),
			})
		}
	}
	return result
}

// GetClosestMarketTarget returns the price to beat for the soonest-ending market.
func (e *Engine) GetClosestMarketTarget() (float64, time.Duration, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now()
	var closestTarget float64
	var closestTime time.Duration = 999 * time.Hour
	found := false

	for _, state := range e.marketStates {
		timeToEnd := state.EndTime.Sub(now)
		if timeToEnd > 0 && timeToEnd < closestTime {
			closestTime = timeToEnd
			closestTarget = state.PriceToBeat
			found = true
		}
	}
	return closestTarget, closestTime, found
}

// GetStats returns current simulation statistics.
func (e *Engine) GetStats() SimulationStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := SimulationStats{
		TotalMarketsObserved: len(e.marketStates) + len(e.skippedMarkets) + len(e.trades),
		TotalTradesEntered:   len(e.trades),
		TotalMarketsSkipped:  len(e.skippedMarkets),
		SkipReasons:          make(map[SkipReason]int),
	}

	for _, trade := range e.trades {
		stats.TotalPnL += trade.PnL
		switch trade.Outcome {
		case OutcomeWin:
			stats.TotalWins++
		case OutcomeLose:
			stats.TotalLosses++
		case OutcomePending:
			stats.TotalPending++
		}
	}

	for _, skip := range e.skippedMarkets {
		stats.SkipReasons[skip.Reason]++
	}

	resolved := stats.TotalWins + stats.TotalLosses
	if resolved > 0 {
		stats.WinRate = float64(stats.TotalWins) / float64(resolved) * 100
	}

	return stats
}

// GetTrades returns all trades.
func (e *Engine) GetTrades() []SimulatedTrade {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]SimulatedTrade, len(e.trades))
	copy(result, e.trades)
	return result
}

// GetSkippedMarkets returns all skipped markets.
func (e *Engine) GetSkippedMarkets() []SkippedMarket {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]SkippedMarket, len(e.skippedMarkets))
	copy(result, e.skippedMarkets)
	return result
}

// FormatTradeReport generates a formatted trade report.
func (e *Engine) FormatTradeReport() string {
	stats := e.GetStats()
	trades := e.GetTrades()
	skipped := e.GetSkippedMarkets()

	report := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════════╗
║                    BTC 5-MIN TRADING SIMULATION                       ║
╠══════════════════════════════════════════════════════════════════════╣
║  Runtime: %s
║  Markets Observed: %d | Traded: %d | Skipped: %d
╠══════════════════════════════════════════════════════════════════════╣
║  PERFORMANCE                                                          ║
║  Wins: %d | Losses: %d | Pending: %d                                  
║  Win Rate: %.1f%% | Total PnL: $%.2f                                  
╠══════════════════════════════════════════════════════════════════════╣
`,
		time.Since(e.startTime).Round(time.Second),
		stats.TotalMarketsObserved,
		stats.TotalTradesEntered,
		stats.TotalMarketsSkipped,
		stats.TotalWins,
		stats.TotalLosses,
		stats.TotalPending,
		stats.WinRate,
		stats.TotalPnL,
	)

	// Skip reasons breakdown
	report += "║  SKIP REASONS                                                         ║\n"
	for reason, count := range stats.SkipReasons {
		report += fmt.Sprintf("║    %-30s: %d\n", reason, count)
	}

	// Trade details
	if len(trades) > 0 {
		report += "╠══════════════════════════════════════════════════════════════════════╣\n"
		report += "║  TRADE HISTORY                                                        ║\n"
		for _, t := range trades {
			outcomeIcon := "⏳"
			if t.Outcome == OutcomeWin {
				outcomeIcon = "✅"
			} else if t.Outcome == OutcomeLose {
				outcomeIcon = "❌"
			}
			priceSource := "sim"
			if t.RealOrderBook {
				priceSource = "REAL"
			}
			report += fmt.Sprintf("║  %s #%d | %s @ $%.2f | Entry: $%.4f [%s] | PnL: $%.2f\n",
				outcomeIcon, t.ID, t.Direction, t.EntryBTCPrice, t.EntryPrice, priceSource, t.PnL)
			report += fmt.Sprintf("║      Reason: %s\n", t.EntryReason)
		}
	}

	// Recent skips (last 5)
	if len(skipped) > 0 {
		report += "╠══════════════════════════════════════════════════════════════════════╣\n"
		report += "║  RECENT SKIPS (last 5)                                               ║\n"
		start := 0
		if len(skipped) > 5 {
			start = len(skipped) - 5
		}
		for _, s := range skipped[start:] {
			report += fmt.Sprintf("║  ⏭️  %s: %s\n", s.Reason, s.Details)
		}
	}

	report += "╚══════════════════════════════════════════════════════════════════════╝\n"

	return report
}
