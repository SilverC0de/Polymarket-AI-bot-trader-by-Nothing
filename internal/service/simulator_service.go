package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silver/pmvibes/internal/simulator"
	"github.com/silver/pmvibes/internal/store"
	"github.com/silver/pmvibes/pkg/polymarket"
)

// MaxFinanceHistoryLimit caps persisted rows returned from GET /finance (history_limit).
const MaxFinanceHistoryLimit = 10000

// HistoryPageSize is the number of events per GET /finance/history/{page}.
const HistoryPageSize int64 = 10

// PriceStaleAfter is how long without a price tick before we treat the feed as
// unhealthy and stop creating/resolving markets against it. The Polymarket
// chainlink BTC stream pushes well under 1Hz, so 15s is generous.
const PriceStaleAfter = 15 * time.Second

// readableTimeLayout matches e.g. "2026-05-03 1:05 am" (12-hour clock, no leading zero on hour).
const readableTimeLayout = "2006-01-02 3:04 pm"

// Polymarket uses Eastern US time for operational/market-facing windows.
var polymarketTZ = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// SimulatorService manages the BTC 5m trading simulation.
type SimulatorService struct {
	mu          sync.RWMutex
	client      *polymarket.Client
	engine      *simulator.Engine
	priceClient *polymarket.PriceClient
	discoverer  *simulator.MarketDiscoverer
	logger      *slog.Logger

	currentPrice float64
	lastPriceAt  time.Time
	running      bool
	startTime    time.Time
	logs         []LogEntry

	liveOrderMu     sync.Mutex
	liveOrderTokens map[string]struct{}

	eventLog store.EventRecorder
}

// LogEntry represents a log message.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

// SimulatorStatus is the response for /finance endpoint.
type SimulatorStatus struct {
	ServerTime     string  `json:"server_time"`
	PolymarketTime string  `json:"polymarket_time"`
	Running        bool    `json:"running"`
	Uptime         string  `json:"uptime"`
	CurrentPrice   float64 `json:"current_btc_price"`
	TargetPrice    float64 `json:"target_price,omitempty"`
	PriceDiff      float64 `json:"price_diff,omitempty"`
	TimeToEnd      string  `json:"time_to_end,omitempty"`
	// Trend is UP, DOWN, or NONE from strategy price history for the soonest-ending active market.
	Trend string `json:"trend,omitempty"`

	// Health of the upstream BTC price feed. PriceAgeSeconds is the number of
	// seconds since the last price tick was received; PriceFeedHealthy is
	// false when the feed is silent for longer than PriceStaleAfter or the
	// websocket has disconnected.
	PriceAgeSeconds  float64                    `json:"price_age_seconds"`
	PriceFeedHealthy bool                       `json:"price_feed_healthy"`
	WSConnected      bool                       `json:"ws_connected"`
	Stats            SimStats                   `json:"stats"`
	Trades           []simulator.SimulatedTrade `json:"trades"`
	Outcomes         []simulator.MarketOutcome  `json:"market_outcomes"`

	// PersistedTotal is the in-memory event count (each append is also logged to stdout).
	PersistedTotal int64 `json:"persisted_total,omitempty"`
	// PersistedEvents is filled when the client passes history_limit on GET /finance.
	PersistedEvents []store.PersistedEvent `json:"persisted_events,omitempty"`
}

// SimStats contains simulation statistics.
type SimStats struct {
	TotalTrades  int            `json:"total_trades"`
	TotalSkipped int            `json:"total_skipped"`
	TotalWins    int            `json:"wins"`
	TotalLosses  int            `json:"losses"`
	TotalPending int            `json:"pending"`
	WinRate      float64        `json:"win_rate"`
	SkipReasons  map[string]int `json:"skip_reasons"`
}

// NewSimulatorService creates a new simulator service.
func NewSimulatorService(logger *slog.Logger, eventLog store.EventRecorder) *SimulatorService {
	client := polymarket.NewClient()
	strategy := simulator.NewStrategy(simulator.DefaultStrategyConfig())
	engine := simulator.NewEngine(strategy, client) // Pass client for real order book prices

	s := &SimulatorService{
		client:          client,
		engine:          engine,
		discoverer:      simulator.NewMarketDiscoverer(client),
		logger:          logger,
		startTime:       time.Now(),
		logs:            make([]LogEntry, 0),
		liveOrderTokens: make(map[string]struct{}),
		eventLog:        eventLog,
	}

	// Wire up live trading if LIVE_TRADING=true and all credentials are present.
	if strings.EqualFold(os.Getenv("LIVE_TRADING"), "true") {
		sigType := polymarket.SigTypePolyProxy // default: Telegram/email/Google (Magic Link)
		if v := os.Getenv("POLYMARKET_SIG_TYPE"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				sigType = n
			}
		}
		cfg := polymarket.TraderConfig{
			PrivateKeyHex: os.Getenv("POLYMARKET_PRIVATE_KEY"),
			ProxyWallet:   os.Getenv("POLYMARKET_PROXY_WALLET"),
			APIKey:        os.Getenv("POLYMARKET_API_KEY"),
			APISecret:     os.Getenv("POLYMARKET_API_SECRET"),
			APIPassphrase: os.Getenv("POLYMARKET_API_PASSPHRASE"),
			SignatureType: sigType,
		}
		trader, err := polymarket.NewTrader(cfg)
		if err != nil {
			logger.Error("live trading disabled — credential error", "err", err)
		} else {
			logger.Info("LIVE TRADING ENABLED")
			engine.SetLiveTradeCallback(func(ctx context.Context, tokenID string, direction simulator.Direction, amountUSD, entryPrice float64) {
				if !s.reserveLiveOrder(tokenID) {
					logger.Warn("duplicate live order skipped", "direction", direction, "tokenID", tokenID)
					return
				}

				result, err := trader.PlaceMarketOrder(ctx, tokenID, amountUSD, entryPrice)
				if err != nil {
					logger.Error("live order FAILED",
						"direction", direction, "tokenID", tokenID,
						"amountUSD", amountUSD, "entryPrice", entryPrice,
						"err", err)
					s.addLog("ERROR", fmt.Sprintf("LIVE ORDER FAILED: %s $%.2f @ %.4f — %v", direction, amountUSD, entryPrice, err))
					return
				}
				logger.Info("live order PLACED",
					"direction", direction, "orderID", result.OrderID, "status", result.Status)
				s.addLog("INFO", fmt.Sprintf("LIVE ORDER PLACED: %s $%.2f @ %.4f — orderID: %s",
					direction, amountUSD, entryPrice, result.OrderID))

				// FAK orders settle almost immediately; poll for the actual fill.
				if result.OrderID == "" {
					return
				}
				go func(orderID string) {
					// Give the CLOB up to ~3 seconds to settle the FAK order.
					ticker := time.NewTicker(500 * time.Millisecond)
					defer ticker.Stop()
					deadline := time.After(3 * time.Second)
					for {
						select {
						case <-deadline:
							s.addLog("WARN", fmt.Sprintf("FILL CHECK TIMEOUT: orderID %s — check polymarket.com/profile manually", orderID))
							return
						case <-ticker.C:
							pollCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							status, err := trader.GetOrderStatus(pollCtx, orderID)
							cancel()
							if err != nil {
								logger.Warn("fill check error", "orderID", orderID, "err", err)
								continue
							}
							if status.Status == "live" {
								continue // not settled yet
							}
							filledUSD := status.FilledUSD()
							filledShares := status.FilledShares()
							fillPrice := status.FillPrice()
							if filledUSD > 0 {
								logger.Info("live order FILLED",
									"direction", direction, "orderID", orderID,
									"filledUSD", filledUSD, "filledShares", filledShares,
									"fillPrice", fillPrice, "status", status.Status)
								s.addLog("INFO", fmt.Sprintf("LIVE ORDER FILLED: %s — $%.4f spent, %.4f shares @ %.4f (status: %s)",
									direction, filledUSD, filledShares, fillPrice, status.Status))
							} else {
								logger.Info("live order CANCELLED (no fill)",
									"direction", direction, "orderID", orderID, "status", status.Status)
								s.addLog("WARN", fmt.Sprintf("LIVE ORDER CANCELLED (no fill): %s orderID %s — insufficient liquidity?",
									direction, orderID))
							}
							return
						}
					}
				}(result.OrderID)
			})
		}
	} else {
		logger.Info("simulation mode — set LIVE_TRADING=true to place real orders")
	}

	// Set up engine callbacks
	engine.SetTradeCallback(func(trade simulator.SimulatedTrade) {
		if trade.Outcome == simulator.OutcomePending {
			s.addLog("INFO", fmt.Sprintf("TRADE ENTERED #%d: %s @ $%.2f, target $%.2f",
				trade.ID, trade.Direction, trade.EntryBTCPrice, trade.PriceToBeat))
		} else {
			s.addLog("INFO", fmt.Sprintf("TRADE RESOLVED #%d: %s, PnL: $%.2f",
				trade.ID, trade.Outcome, trade.PnL))
		}
		s.persistEvent("trade", trade)
	})

	engine.SetSkipCallback(func(skip simulator.SkippedMarket) {
		s.addLog("DEBUG", fmt.Sprintf("SKIPPED: %s - %s", skip.Reason, skip.Details))
		s.persistEvent("skip", skip)
	})

	engine.SetMarketEndCallback(func(outcome simulator.MarketOutcome) {
		result := "UP"
		if outcome.Result == simulator.DirectionDown {
			result = "DOWN"
		}
		s.addLog("INFO", fmt.Sprintf("ROUND ENDED: %s, Final: $%.2f, Target: $%.2f",
			result, outcome.FinalPrice, outcome.PriceToBeat))
		s.persistEvent("outcome", outcome)
	})

	return s
}

func (s *SimulatorService) reserveLiveOrder(tokenID string) bool {
	s.liveOrderMu.Lock()
	defer s.liveOrderMu.Unlock()

	if _, exists := s.liveOrderTokens[tokenID]; exists {
		return false
	}
	s.liveOrderTokens[tokenID] = struct{}{}
	return true
}

func (s *SimulatorService) persistEvent(kind string, data any) {
	if s.eventLog == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.eventLog.Append(ctx, kind, data); err != nil {
		s.logger.Error("persist simulation event", "err", err, "kind", kind)
	}
}

func (s *SimulatorService) addLog(level, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
	}
	s.logs = append(s.logs, entry)

	// Keep only last 100 logs
	if len(s.logs) > 100 {
		s.logs = s.logs[len(s.logs)-100:]
	}
}

// Start begins the simulation.
func (s *SimulatorService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("simulator already running")
	}
	s.running = true
	s.startTime = time.Now()
	s.mu.Unlock()

	s.addLog("INFO", "Simulator starting...")

	// Connect to price stream
	s.priceClient = polymarket.NewPriceClient(func(price polymarket.PriceUpdate) {
		s.mu.Lock()
		s.currentPrice = price.Value
		s.lastPriceAt = time.Now()
		s.mu.Unlock()
		ts := time.UnixMilli(price.Timestamp)
		s.engine.ProcessPriceUpdate(price.Value, ts)
		// Record price for boundary capture (to get accurate "price to beat")
		s.discoverer.RecordPrice(price.Value, ts)
	}, s.logger)

	if err := s.priceClient.Connect(ctx); err != nil {
		s.addLog("ERROR", fmt.Sprintf("Failed to connect to price stream: %v", err))
		return err
	}
	s.addLog("INFO", "Connected to Polymarket price stream")

	// Start market discovery
	go s.marketDiscoveryLoop(ctx)

	// Start market resolution
	go s.marketResolutionLoop(ctx)

	return nil
}

func (s *SimulatorService) marketDiscoveryLoop(ctx context.Context) {
	// Poll every 2 seconds to catch window starts accurately
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	s.discoverMarkets(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.discoverMarkets(ctx)
		}
	}
}

func (s *SimulatorService) discoverMarkets(ctx context.Context) {
	s.mu.RLock()
	currentPrice := s.currentPrice
	lastAt := s.lastPriceAt
	s.mu.RUnlock()

	if currentPrice == 0 {
		return
	}
	// Don't poison new markets with a stale price as the fallback PriceToBeat.
	if lastAt.IsZero() || time.Since(lastAt) > PriceStaleAfter {
		return
	}

	markets, err := s.discoverer.DiscoverMarkets(ctx, currentPrice)
	if err != nil {
		return
	}

	for _, market := range markets {
		state := s.engine.GetOrCreateMarketState(market.MarketID, market.PriceToBeat, market.StartTime, market.EndTime)
		if state != nil {
			s.addLog("INFO", fmt.Sprintf("Tracking market: %s, Target: $%.2f, Ends: %s",
				market.EventTitle, market.PriceToBeat, market.EndTime.Format("15:04:05")))

			// Fetch token IDs for real order book pricing
			go s.fetchAndSetTokenIDs(ctx, market.MarketID)
		}
	}
}

// fetchAndSetTokenIDs fetches the token IDs for a market and sets them on the engine.
func (s *SimulatorService) fetchAndSetTokenIDs(ctx context.Context, marketID string) {
	events, err := s.client.SearchActiveBTC5mMarkets(ctx)
	if err != nil {
		return
	}

	for _, event := range events {
		for _, mkt := range event.Markets {
			if mkt.ID == marketID && mkt.ClobTokenIDs != "" {
				upToken, downToken, err := polymarket.ParseClobTokenIDs(mkt.ClobTokenIDs)
				if err == nil && upToken != "" && downToken != "" {
					s.engine.SetMarketTokenIDs(marketID, upToken, downToken)
					s.addLog("DEBUG", fmt.Sprintf("Set token IDs for market %s", marketID))
				}
				return
			}
		}
	}
}

func (s *SimulatorService) extractPriceToBeat(text string) float64 {
	if text == "" {
		return 0
	}
	re := regexp.MustCompile(`\$([0-9,]+\.?\d*)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		priceStr := strings.ReplaceAll(matches[1], ",", "")
		price, _ := strconv.ParseFloat(priceStr, 64)
		if price > 10000 {
			return price
		}
	}
	return 0
}

func (s *SimulatorService) marketResolutionLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.RLock()
			price := s.currentPrice
			lastAt := s.lastPriceAt
			s.mu.RUnlock()

			// Refuse to resolve markets with a stale price; that's how the
			// previous bug ended up recording 24h of FinalPrice == PriceToBeat
			// "UP" outcomes after the websocket silently died.
			if lastAt.IsZero() || time.Since(lastAt) > PriceStaleAfter {
				continue
			}

			for _, market := range s.engine.GetActiveMarkets() {
				if now.After(market.EndTime) {
					s.engine.ResolveMarket(market.MarketID, price)
				}
			}
			for _, trade := range s.engine.GetTrades() {
				if trade.Outcome == simulator.OutcomePending && now.After(trade.MarketEndTime) {
					s.engine.ResolveMarket(trade.MarketID, price)
				}
			}
		}
	}
}

// GetStatus returns the current simulation status.
// When historyLimit > 0, PersistedTotal and PersistedEvents are filled from the configured event log (newest first).
func (s *SimulatorService) GetStatus(ctx context.Context, historyLimit int) SimulatorStatus {
	s.mu.RLock()
	currentPrice := s.currentPrice
	running := s.running
	startTime := s.startTime
	lastPriceAt := s.lastPriceAt
	priceClient := s.priceClient
	s.mu.RUnlock()

	var priceAge float64
	healthy := false
	if !lastPriceAt.IsZero() {
		age := time.Since(lastPriceAt)
		priceAge = age.Seconds()
		healthy = age <= PriceStaleAfter
	}
	wsConnected := false
	if priceClient != nil {
		wsConnected = priceClient.Connected()
	}

	stats := s.engine.GetStats()
	target, timeToEnd, hasTarget := s.engine.GetClosestMarketTarget()

	now := time.Now()
	status := SimulatorStatus{
		ServerTime:       now.Format(readableTimeLayout),
		PolymarketTime:   now.In(polymarketTZ).Format(readableTimeLayout),
		Running:          running,
		Uptime:           time.Since(startTime).Round(time.Second).String(),
		CurrentPrice:     currentPrice,
		PriceAgeSeconds:  priceAge,
		PriceFeedHealthy: healthy,
		WSConnected:      wsConnected,
		Stats: SimStats{
			TotalTrades:  stats.TotalTradesEntered,
			TotalSkipped: stats.TotalMarketsSkipped,
			TotalWins:    stats.TotalWins,
			TotalLosses:  stats.TotalLosses,
			TotalPending: stats.TotalPending,
			WinRate:      stats.WinRate,
			SkipReasons:  make(map[string]int),
		},
		Trades:   s.engine.GetTrades(),
		Outcomes: s.engine.GetMarketOutcomes(),
	}

	for reason, count := range stats.SkipReasons {
		status.Stats.SkipReasons[string(reason)] = count
	}

	if hasTarget {
		status.TargetPrice = target
		status.PriceDiff = currentPrice - target
		status.TimeToEnd = timeToEnd.Round(time.Second).String()
	}

	trendDir := s.engine.GetTrendForClosestMarket()
	if trendDir != simulator.DirectionNone {
		status.Trend = string(trendDir)
	}

	if historyLimit > 0 && s.eventLog != nil {
		if historyLimit > MaxFinanceHistoryLimit {
			historyLimit = MaxFinanceHistoryLimit
		}
		if total, err := s.eventLog.Len(ctx); err == nil {
			status.PersistedTotal = total
		}
		if evs, err := s.eventLog.ListRecent(ctx, int64(historyLimit)); err == nil {
			status.PersistedEvents = stripPersistedPnL(evs)
		}
	}

	return status
}

// PersistedRecent returns newest persisted events (newest first).
func (s *SimulatorService) PersistedRecent(ctx context.Context, limit int64) ([]store.PersistedEvent, error) {
	if s.eventLog == nil {
		return nil, nil
	}
	evs, err := s.eventLog.ListRecent(ctx, limit)
	return stripPersistedPnL(evs), err
}

// PersistedPaged returns events by offset from newest (offset 0 = newest row).
func (s *SimulatorService) PersistedPaged(ctx context.Context, offset, limit int64) ([]store.PersistedEvent, error) {
	if s.eventLog == nil {
		return nil, nil
	}
	evs, err := s.eventLog.ListRange(ctx, offset, limit)
	return stripPersistedPnL(evs), err
}

// PersistedHistoryPaged returns one page of persisted events (newest first).
// If learn is false, only trade events with outcome WIN or LOSE are included; skips, outcomes, and pending trades are omitted.
// If learn is true, behavior matches PersistedPaged (all event kinds).
func (s *SimulatorService) PersistedHistoryPaged(ctx context.Context, offset, limit int64, learn bool) ([]store.PersistedEvent, int64, error) {
	if s.eventLog == nil {
		return nil, 0, nil
	}
	if learn {
		total, err := s.eventLog.Len(ctx)
		if err != nil {
			return nil, 0, err
		}
		evs, err := s.eventLog.ListRange(ctx, offset, limit)
		return stripPersistedPnL(evs), total, err
	}
	n, err := s.eventLog.Len(ctx)
	if err != nil {
		return nil, 0, err
	}
	all, err := s.eventLog.ListRange(ctx, 0, n)
	if err != nil {
		return nil, 0, err
	}
	filtered := store.FilterPersistedForHistory(all, false)
	total := int64(len(filtered))
	if offset >= total {
		return []store.PersistedEvent{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return stripPersistedPnL(filtered[offset:end]), total, nil
}

func stripPersistedPnL(events []store.PersistedEvent) []store.PersistedEvent {
	if len(events) == 0 {
		return events
	}
	out := make([]store.PersistedEvent, len(events))
	copy(out, events)
	for i := range out {
		var payload any
		if err := json.Unmarshal(out[i].Data, &payload); err != nil {
			continue
		}
		removePnLKeys(payload)
		if data, err := json.Marshal(payload); err == nil {
			out[i].Data = data
		}
	}
	return out
}

func removePnLKeys(v any) {
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			if isPnLKey(key) {
				delete(x, key)
				continue
			}
			removePnLKeys(value)
		}
	case []any:
		for _, value := range x {
			removePnLKeys(value)
		}
	}
}

func isPnLKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	return normalized == "pnl" || normalized == "ourpnl" || normalized == "totalpnl"
}

// PersistedLen returns total persisted events held in memory for this process.
func (s *SimulatorService) PersistedLen(ctx context.Context) (int64, error) {
	if s.eventLog == nil {
		return 0, nil
	}
	return s.eventLog.Len(ctx)
}

// Stop stops the simulation.
func (s *SimulatorService) Stop() {
	s.mu.Lock()
	if s.priceClient != nil {
		s.priceClient.Close()
		s.priceClient = nil
	}
	s.running = false
	s.mu.Unlock()

	s.addLog("INFO", "Simulator stopped")
}
