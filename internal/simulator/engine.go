package simulator

import (
	"context"
	"fmt"
	"math"
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

const (
	ExperimentalWindow = 30 * time.Second
	ExperimentalDiff   = 30.0
	ExperimentalSpike  = 5.0
	ExperimentalMinAvg = 0.96
	ExperimentalSize   = 1.0
)

// TradeDebugContext contains diagnostic info for analyzing losing trades.
type TradeDebugContext struct {
	PriceHistory    []PriceSnapshot `json:"price_history"`     // Price samples from entry to resolution
	EntryMomentum   float64         `json:"entry_momentum"`    // Momentum at entry ($/sec)
	EntryRecentMove float64         `json:"entry_recent_move"` // How much price moved before entry ($)
}

// SimulatedTrade represents a simulated trade entry.
type SimulatedTrade struct {
	ID            int
	MarketID      string
	EntryTime     time.Time
	MarketEndTime time.Time
	Direction     Direction // UP or DOWN
	PriceToBeat   float64   // BTC price at market start
	EntryBTCPrice float64   // BTC price when trade was entered
	EntryReason   string
	TradeSize     float64            // USD amount ($10)
	StrategyLabel string             // "default" or "experimental"
	EntryPrice    float64            // Price paid for the outcome token (e.g., 0.55)
	RealOrderBook bool               // True if EntryPrice came from real order book, false if simulated
	Outcome       TradeOutcome       // WIN, LOSE, or PENDING
	FinalBTCPrice float64            // BTC price at market end
	PnL           float64            `json:"-"`                       // Profit/Loss in USD
	DebugContext  *TradeDebugContext `json:"debug_context,omitempty"` // Only populated for losing trades
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

// ExperimentalMarketDebug exposes per-market diagnostics for the experimental strategy.
type ExperimentalMarketDebug struct {
	MarketID            string     `json:"market_id"`
	TimeToEnd           string     `json:"time_to_end"`
	PriceToBeat         float64    `json:"price_to_beat"`
	PolymarketPrice     float64    `json:"polymarket_price"`
	CoinbasePrice       float64    `json:"coinbase_price"`
	PolymarketDiff      float64    `json:"polymarket_diff"`
	CoinbaseDiff        float64    `json:"coinbase_diff"`
	Direction           Direction  `json:"direction"`
	HasPendingTrade     bool       `json:"has_pending_trade"`
	WithinLast30s       bool       `json:"within_last_30s"`
	DualFeed30Qualified bool       `json:"dual_feed_30_qualified"`
	SameDirection       bool       `json:"same_direction"`
	AvgPriceQualified   bool       `json:"avg_price_qualified"`
	Armed               bool       `json:"armed"`
	BaseCoinbasePrice   float64    `json:"base_coinbase_price"`
	SpikeFromBase       float64    `json:"spike_from_base"`
	SpikeQualified      bool       `json:"spike_qualified"`
	LastOrderbookCheck  string     `json:"last_orderbook_check,omitempty"`
	EnteredTrade        bool       `json:"entered_trade"`
	SkipReason          SkipReason `json:"skip_reason,omitempty"`
	BlockedReason       string     `json:"blocked_reason,omitempty"`
}

// MarketOutcome records the result of a 5-minute market.
type MarketOutcome struct {
	MarketID     string
	EndTime      time.Time
	PriceToBeat  float64
	FinalPrice   float64
	Result       Direction // UP or DOWN
	PriceDiff    float64
	WeTradedIt   bool
	OurDirection Direction
	OurPnL       float64 `json:"-"`
}

// LiveTradeFunc is called immediately when the strategy fires a signal, before
// the simulated trade is recorded.  tokenID is the outcome token to buy.
// The function runs in its own goroutine; errors should be logged by the caller.
type LiveTradeFunc func(ctx context.Context, tokenID string, direction Direction, amountUSD, entryPrice float64)

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
	onLiveTrade    LiveTradeFunc // nil when not in live-trading mode
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

// SetLiveTradeCallback registers the function called when a strategy signal fires.
// Pass nil to disable live trading and revert to simulation-only mode.
func (e *Engine) SetLiveTradeCallback(fn LiveTradeFunc) {
	e.onLiveTrade = fn
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

	for _, state := range e.marketStates {
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
				MarketID:     state.MarketID,
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
			e.enterTradeLocked(state, direction, btcPrice, timestamp, reason, e.strategy.config.TradeSize, "default")
		}
	}
}

// ProcessCoinbaseUpdate checks late-window experimental entries based on Coinbase spikes.
func (e *Engine) ProcessCoinbaseUpdate(coinbasePrice, polymarketPrice float64, timestamp time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if coinbasePrice <= 0 || polymarketPrice <= 0 || e.hasPendingTradeLocked() {
		return
	}

	for _, state := range e.marketStates {
		if state.EnteredTrade {
			continue
		}

		timeToEnd := state.EndTime.Sub(timestamp)
		if timeToEnd <= 0 || timeToEnd > ExperimentalWindow {
			state.ExperimentalArmed = false
			state.ExperimentalAvgPriceOK = false
			continue
		}

		pmDiff := polymarketPrice - state.PriceToBeat
		cbDiff := coinbasePrice - state.PriceToBeat
		if math.Abs(pmDiff) < ExperimentalDiff || math.Abs(cbDiff) < ExperimentalDiff {
			state.ExperimentalArmed = false
			state.ExperimentalAvgPriceOK = false
			continue
		}

		direction := DirectionUp
		if cbDiff < 0 {
			direction = DirectionDown
		}
		if (pmDiff > 0 && cbDiff < 0) || (pmDiff < 0 && cbDiff > 0) {
			state.ExperimentalArmed = false
			state.ExperimentalAvgPriceOK = false
			continue
		}

		if !state.ExperimentalArmed || state.ExperimentalDirection != direction {
			state.ExperimentalArmed = true
			state.ExperimentalDirection = direction
			state.ExperimentalBaseCoinbase = coinbasePrice
			state.ExperimentalAvgPriceOK = false
			state.ExperimentalLastOBCheck = time.Time{}
		}

		if timestamp.Sub(state.ExperimentalLastOBCheck) >= time.Second {
			state.ExperimentalLastOBCheck = timestamp
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			entryPrice, ok := e.getRealEntryPrice(ctx, state, direction, ExperimentalSize)
			cancel()
			state.ExperimentalAvgPriceOK = ok && entryPrice >= ExperimentalMinAvg
		}
		if !state.ExperimentalAvgPriceOK {
			continue
		}

		spike := coinbasePrice - state.ExperimentalBaseCoinbase
		if direction == DirectionDown {
			spike = state.ExperimentalBaseCoinbase - coinbasePrice
		}
		if spike < ExperimentalSpike {
			continue
		}

		reason := fmt.Sprintf("experimental: dual-feed >=$30 from target, avg>=%.2f, coinbase spike $%.2f", ExperimentalMinAvg, spike)
		e.enterTradeLocked(state, direction, coinbasePrice, timestamp, reason, ExperimentalSize, "experimental")
	}
}

func (e *Engine) recordSkip(state *MarketState, timestamp time.Time, reason SkipReason, details string, btcPrice float64) {
	state.SkipReason = reason
	skip := SkippedMarket{
		MarketID:     state.MarketID,
		Timestamp:    timestamp,
		Reason:       reason,
		Details:      details,
		BTCPrice:     btcPrice,
		PriceToBeat:  state.PriceToBeat,
		TimeToEnd:    state.EndTime.Sub(timestamp),
		PriceHistory: state.PriceHistory,
	}
	e.skippedMarkets = append(e.skippedMarkets, skip)
	if e.onSkip != nil {
		go e.onSkip(skip)
	}
}

func potentialProfit(amountUSD, entryPrice float64) float64 {
	if amountUSD <= 0 || entryPrice <= 0 {
		return 0
	}
	shares := amountUSD / entryPrice
	return (1 - entryPrice) * shares
}

func (e *Engine) hasPendingTradeLocked() bool {
	for _, trade := range e.trades {
		if trade.Outcome == OutcomePending {
			return true
		}
	}
	return false
}

// GetExperimentalDebug returns per-market experimental strategy diagnostics.
func (e *Engine) GetExperimentalDebug(polymarketPrice, coinbasePrice float64, now time.Time) []ExperimentalMarketDebug {
	e.mu.RLock()
	defer e.mu.RUnlock()

	hasPending := e.hasPendingTradeLocked()
	out := make([]ExperimentalMarketDebug, 0, len(e.marketStates))

	for _, state := range e.marketStates {
		timeToEnd := state.EndTime.Sub(now)
		if timeToEnd <= 0 || timeToEnd > ExperimentalWindow {
			continue
		}
		pmDiff := polymarketPrice - state.PriceToBeat
		cbDiff := coinbasePrice - state.PriceToBeat
		dualFeedOK := math.Abs(pmDiff) >= ExperimentalDiff && math.Abs(cbDiff) >= ExperimentalDiff
		sameDirection := (pmDiff > 0 && cbDiff > 0) || (pmDiff < 0 && cbDiff < 0)
		withinWindow := true

		direction := DirectionNone
		if cbDiff > 0 {
			direction = DirectionUp
		} else if cbDiff < 0 {
			direction = DirectionDown
		}

		spike := 0.0
		if state.ExperimentalArmed {
			spike = coinbasePrice - state.ExperimentalBaseCoinbase
			if state.ExperimentalDirection == DirectionDown {
				spike = state.ExperimentalBaseCoinbase - coinbasePrice
			}
		}
		spikeOK := spike >= ExperimentalSpike

		blocked := ""
		switch {
		case state.EnteredTrade:
			blocked = "already_entered_trade"
		case hasPending:
			blocked = "pending_trade_exists"
		case !withinWindow:
			blocked = "outside_last_30s_window"
		case polymarketPrice <= 0 || coinbasePrice <= 0:
			blocked = "missing_spot_price"
		case !dualFeedOK:
			blocked = "dual_feed_not_30_from_target"
		case !sameDirection:
			blocked = "feeds_not_same_direction"
		case !state.ExperimentalAvgPriceOK:
			blocked = "avg_price_below_98c_or_unavailable"
		case !state.ExperimentalArmed:
			blocked = "not_armed"
		case !spikeOK:
			blocked = "coinbase_spike_below_5"
		default:
			blocked = ""
		}

		lastOB := ""
		if !state.ExperimentalLastOBCheck.IsZero() {
			lastOB = state.ExperimentalLastOBCheck.UTC().Format(time.RFC3339)
		}

		out = append(out, ExperimentalMarketDebug{
			MarketID:            state.MarketID,
			TimeToEnd:           timeToEnd.Round(time.Second).String(),
			PriceToBeat:         state.PriceToBeat,
			PolymarketPrice:     polymarketPrice,
			CoinbasePrice:       coinbasePrice,
			PolymarketDiff:      pmDiff,
			CoinbaseDiff:        cbDiff,
			Direction:           direction,
			HasPendingTrade:     hasPending,
			WithinLast30s:       withinWindow,
			DualFeed30Qualified: dualFeedOK,
			SameDirection:       sameDirection,
			AvgPriceQualified:   state.ExperimentalAvgPriceOK,
			Armed:               state.ExperimentalArmed,
			BaseCoinbasePrice:   state.ExperimentalBaseCoinbase,
			SpikeFromBase:       spike,
			SpikeQualified:      spikeOK,
			LastOrderbookCheck:  lastOB,
			EnteredTrade:        state.EnteredTrade,
			SkipReason:          state.SkipReason,
			BlockedReason:       blocked,
		})
	}

	return out
}

func (e *Engine) enterTradeLocked(state *MarketState, direction Direction, btcPrice float64, timestamp time.Time, reason string, tradeSize float64, strategyLabel string) bool {
	var entryPrice float64
	var realOrderBook bool
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if realPrice, ok := e.getRealEntryPrice(ctx, state, direction, tradeSize); ok {
		entryPrice = realPrice
		realOrderBook = true
	} else {
		entryPrice = e.calculateEntryPrice(btcPrice, state.PriceToBeat, direction)
		realOrderBook = false
	}
	cancel()

	if entryPrice > e.strategy.config.MaxEntryPrice {
		e.recordSkip(state, timestamp, SkipNoLiquidity,
			fmt.Sprintf("entry price %.4f above max %.4f", entryPrice, e.strategy.config.MaxEntryPrice),
			btcPrice)
		return false
	}
	if strategyLabel != "experimental" {
		if profit := potentialProfit(tradeSize, entryPrice); profit < e.strategy.config.MinProfitUSD {
			e.recordSkip(state, timestamp, SkipNoLiquidity,
				fmt.Sprintf("win profit $%.2f below minimum $%.2f at entry price %.4f", profit, e.strategy.config.MinProfitUSD, entryPrice),
				btcPrice)
			return false
		}
	}

	e.tradeCounter++
	state.EnteredTrade = true
	state.TradeDirection = direction

	entryMomentum := e.strategy.calculateMomentum(state.PriceHistory, state.PriceToBeat)
	entryRecentMove := e.strategy.calculateRecentMove(state.PriceHistory, direction)

	trade := SimulatedTrade{
		ID:            e.tradeCounter,
		MarketID:      state.MarketID,
		EntryTime:     timestamp,
		MarketEndTime: state.EndTime,
		Direction:     direction,
		PriceToBeat:   state.PriceToBeat,
		EntryBTCPrice: btcPrice,
		EntryReason:   reason,
		TradeSize:     tradeSize,
		StrategyLabel: strategyLabel,
		EntryPrice:    entryPrice,
		RealOrderBook: realOrderBook,
		Outcome:       OutcomePending,
		DebugContext: &TradeDebugContext{
			EntryMomentum:   entryMomentum,
			EntryRecentMove: entryRecentMove,
			PriceHistory:    make([]PriceSnapshot, len(state.PriceHistory)),
		},
	}
	copy(trade.DebugContext.PriceHistory, state.PriceHistory)
	e.trades = append(e.trades, trade)

	if e.onTradeUpdate != nil {
		go e.onTradeUpdate(trade)
	}
	if e.onLiveTrade != nil {
		tokenID := state.UpTokenID
		if direction == DirectionDown {
			tokenID = state.DownTokenID
		}
		if tokenID != "" {
			go e.onLiveTrade(context.Background(), tokenID, direction, tradeSize, entryPrice)
		}
	}
	return true
}

// calculateEntryPrice simulates the entry price when the order book is unavailable.
// Maps abs diff from [MinPriceDiff, MaxPriceDiff] to entry probability ~0.50–0.65.
func (e *Engine) calculateEntryPrice(btcPrice, priceToBeat float64, _ Direction) float64 {
	diff := btcPrice - priceToBeat
	absDiff := diff
	if absDiff < 0 {
		absDiff = -absDiff
	}

	minD := e.strategy.config.MinPriceDiff
	maxD := e.strategy.config.MaxPriceDiff
	span := maxD - minD
	if span <= 0 {
		span = 1
	}
	ratio := (absDiff - minD) / span
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}

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

// GetTrendForClosestMarket returns the strategy trend (UP/DOWN/NONE) for the
// soonest-ending active market — the same market used for target price on /finance.
func (e *Engine) GetTrendForClosestMarket() Direction {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now()
	var closest *MarketState
	var closestTime time.Duration = 999 * time.Hour
	for _, state := range e.marketStates {
		timeToEnd := state.EndTime.Sub(now)
		if timeToEnd > 0 && timeToEnd < closestTime {
			closestTime = timeToEnd
			closest = state
		}
	}
	if closest == nil {
		return DirectionNone
	}
	return e.strategy.determineTrend(closest.PriceHistory)
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
