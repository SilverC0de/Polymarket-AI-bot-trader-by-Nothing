package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/silver/pmvibes/internal/simulator"
	"github.com/silver/pmvibes/pkg/polymarket"
)

const (
	banner = `
╔══════════════════════════════════════════════════════════════════════╗
║          POLYMARKET BTC 5-MIN TRADING SIMULATOR                      ║
║                                                                      ║
║  Strategy Rules:                                                     ║
║  • Price diff from target: $40–$120 (DefaultStrategyConfig)          ║
║  • Entry window: 30s-100s before market end                         ║
║  • Skip sideways markets (price crossed target both ways)           ║
║  • Follow trend: UP trend → bet UP, DOWN trend → bet DOWN           ║
║  • Trade size: $10 per trade                                        ║
╚══════════════════════════════════════════════════════════════════════╝
`
)

// Atomic storage for current BTC price (thread-safe)
var currentBTCPriceAtomic uint64

func setCurrentPrice(price float64) {
	atomic.StoreUint64(&currentBTCPriceAtomic, uint64(price*100))
}

func getCurrentPrice() float64 {
	return float64(atomic.LoadUint64(&currentBTCPriceAtomic)) / 100
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	fmt.Print(banner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\nShutting down gracefully...")
		cancel()
	}()

	// Initialize components
	client := polymarket.NewClient()
	strategy := simulator.NewStrategy(simulator.DefaultStrategyConfig())
	engine := simulator.NewEngine(strategy, client) // Pass client for real order book prices

	// Set up callbacks for live output
	engine.SetTradeCallback(func(trade simulator.SimulatedTrade) {
		if trade.Outcome == simulator.OutcomePending {
			priceSource := "[simulated]"
			if trade.RealOrderBook {
				priceSource = "[REAL ORDER BOOK]"
			}
			fmt.Printf("\n🔔 TRADE ENTERED #%d\n", trade.ID)
			fmt.Printf("   Direction: %s | BTC: $%.2f | Target: $%.2f\n",
				trade.Direction, trade.EntryBTCPrice, trade.PriceToBeat)
			fmt.Printf("   Entry Price: $%.4f %s | Size: $%.2f\n",
				trade.EntryPrice, priceSource, trade.TradeSize)
			fmt.Printf("   Reason: %s\n", trade.EntryReason)
			fmt.Printf("   Market ends: %s\n", trade.MarketEndTime.Format("15:04:05"))
		} else {
			icon := "✅"
			if trade.Outcome == simulator.OutcomeLose {
				icon = "❌"
			}
			fmt.Printf("\n%s TRADE RESOLVED #%d: %s\n", icon, trade.ID, trade.Outcome)
			fmt.Printf("   Final BTC: $%.2f | PnL: $%.2f\n",
				trade.FinalBTCPrice, trade.PnL)
		}
	})

	engine.SetSkipCallback(func(skip simulator.SkippedMarket) {
		diff := skip.BTCPrice - skip.PriceToBeat
		fmt.Printf("\n⏭️  SKIPPED: %s\n", skip.Reason)
		fmt.Printf("   %s\n", skip.Details)
		fmt.Printf("   BTC: $%.2f | Target: $%.2f | Diff: %+.2f | Time to end: %s\n",
			skip.BTCPrice, skip.PriceToBeat, diff, formatDuration(skip.TimeToEnd))
	})

	// Show market outcomes (every 5-minute round result)
	engine.SetMarketEndCallback(func(outcome simulator.MarketOutcome) {
		icon := "🔵"
		direction := "UP"
		if outcome.Result == simulator.DirectionDown {
			icon = "🔴"
			direction = "DOWN"
		}

		fmt.Printf("\n\n%s ROUND ENDED: %s\n", icon, direction)
		fmt.Printf("   Target: $%.2f | Final: $%.2f | Diff: %+.2f\n",
			outcome.PriceToBeat, outcome.FinalPrice, outcome.PriceDiff)

		if outcome.WeTradedIt {
			resultIcon := "✅ WON"
			if outcome.OurPnL < 0 {
				resultIcon = "❌ LOST"
			}
			fmt.Printf("   Our bet: %s → %s (PnL: $%.2f)\n",
				outcome.OurDirection, resultIcon, outcome.OurPnL)
		} else {
			fmt.Printf("   We did not trade this round\n")
		}
		fmt.Println()
	})

	// Create market discoverer (needs to receive price updates for boundary capture)
	discoverer := simulator.NewMarketDiscoverer(client)

	// Connect to real-time price stream
	priceClient := polymarket.NewPriceClient(func(price polymarket.PriceUpdate) {
		setCurrentPrice(price.Value)
		ts := time.UnixMilli(price.Timestamp)
		engine.ProcessPriceUpdate(price.Value, ts)
		// Record price for boundary capture (to get accurate "price to beat")
		discoverer.RecordPrice(price.Value, ts)
	}, logger)

	fmt.Println("\n📡 Connecting to Polymarket real-time price stream...")
	if err := priceClient.Connect(ctx); err != nil {
		logger.Error("failed to connect to price stream", "err", err)
		fmt.Println("⚠️  Running in demo mode with simulated prices")
		go runDemoMode(ctx, engine)
	}
	defer priceClient.Close()

	// Market discovery loop
	fmt.Println("🔍 Searching for active BTC 5-minute markets...")
	go marketDiscoveryLoopWithDiscoverer(ctx, discoverer, engine)

	// Live price display loop
	go livePriceDisplayLoop(ctx, engine)

	// Market resolution checker
	go marketResolutionLoop(ctx, engine, getCurrentPrice)

	// Wait for shutdown
	<-ctx.Done()

	// Final report
	fmt.Println("\n" + engine.FormatTradeReport())
}

// marketDiscoveryLoopWithDiscoverer periodically searches for new BTC 5m markets.
func marketDiscoveryLoopWithDiscoverer(ctx context.Context, discoverer *simulator.MarketDiscoverer, engine *simulator.Engine) {
	// Search every 1 second to catch markets quickly after boundary price is captured
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Initial search
	discoverMarkets(ctx, discoverer, engine)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			discoverMarkets(ctx, discoverer, engine)
		}
	}
}

func discoverMarkets(ctx context.Context, discoverer *simulator.MarketDiscoverer, engine *simulator.Engine) {
	currentPrice := getCurrentPrice()
	if currentPrice == 0 {
		return
	}

	markets, err := discoverer.DiscoverMarkets(ctx, currentPrice)
	if err != nil {
		return
	}

	now := time.Now()
	newMarketsFound := 0

	for _, market := range markets {
		// Register market with engine (only stores price on first call)
		state := engine.GetOrCreateMarketState(
			market.MarketID,
			market.PriceToBeat,
			market.StartTime,
			market.EndTime,
		)

		if state != nil {
			newMarketsFound++
			timeToEnd := market.EndTime.Sub(now)
			diff := currentPrice - market.PriceToBeat

			fmt.Printf("\n📍 Market: %s\n", market.EventTitle)
			fmt.Printf("   🎯 Target: $%.2f | Current: $%.2f | Diff: %+.2f\n", market.PriceToBeat, currentPrice, diff)
			fmt.Printf("   ⏱️  Ends: %s (in %s)\n", market.EndTime.Format("15:04:05"), formatDuration(timeToEnd))

			// Fetch token IDs for real order book pricing (in background)
			go fetchAndSetTokenIDs(ctx, discoverer, engine, market.MarketID)
		}
	}

	if newMarketsFound > 0 {
		fmt.Printf("\n📍 Found %d BTC 5m market(s) to watch\n", newMarketsFound)
	}
}

// fetchAndSetTokenIDs fetches the token IDs for a market and sets them on the engine.
func fetchAndSetTokenIDs(ctx context.Context, discoverer *simulator.MarketDiscoverer, engine *simulator.Engine, marketID string) {
	client := polymarket.NewClient()
	events, err := client.SearchActiveBTC5mMarkets(ctx)
	if err != nil {
		return
	}

	for _, event := range events {
		for _, mkt := range event.Markets {
			if mkt.ID == marketID && mkt.ClobTokenIDs != "" {
				upToken, downToken, err := polymarket.ParseClobTokenIDs(mkt.ClobTokenIDs)
				if err == nil && upToken != "" && downToken != "" {
					engine.SetMarketTokenIDs(marketID, upToken, downToken)
					fmt.Printf("   📊 Real order book pricing enabled for market %s\n", marketID[:8])
				}
				return
			}
		}
	}
}

// extractPriceToBeat parses the BTC price target from text.
func extractPriceToBeat(text string) float64 {
	if text == "" {
		return 0
	}

	// Pattern 1: "$94,500.00" or "$94500"
	re := regexp.MustCompile(`\$([0-9,]+\.?\d*)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		priceStr := strings.ReplaceAll(matches[1], ",", "")
		price, _ := strconv.ParseFloat(priceStr, 64)
		if price > 10000 { // Sanity check for BTC price
			return price
		}
	}

	// Pattern 2: "≥ 94500" or ">= 94500"
	re2 := regexp.MustCompile(`[≥>=]\s*([0-9,]+\.?\d*)`)
	matches = re2.FindStringSubmatch(text)
	if len(matches) >= 2 {
		priceStr := strings.ReplaceAll(matches[1], ",", "")
		price, _ := strconv.ParseFloat(priceStr, 64)
		if price > 10000 {
			return price
		}
	}

	return 0
}

// livePriceDisplayLoop shows live BTC price and stats.
func livePriceDisplayLoop(ctx context.Context, engine *simulator.Engine) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastPrice := 0.0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			price := getCurrentPrice()
			if price == 0 {
				continue
			}

			// Calculate price change direction
			changeIcon := ""
			if lastPrice > 0 {
				diff := price - lastPrice
				if diff > 0 {
					changeIcon = "↑"
				} else if diff < 0 {
					changeIcon = "↓"
				} else {
					changeIcon = "→"
				}
			}
			lastPrice = price

			// Get target info
			target, timeToEnd, hasTarget := engine.GetClosestMarketTarget()

			// Get stats
			stats := engine.GetStats()

			// Build display string
			if hasTarget && timeToEnd > 0 {
				diff := price - target
				absDiff := diff
				if absDiff < 0 {
					absDiff = -absDiff
				}
				diffSign := "+"
				if diff < 0 {
					diffSign = ""
				}

				// Check conditions (30s - 100s window)
				diffOK := absDiff >= 30 && absDiff <= 60
				timeOK := timeToEnd >= 30*time.Second && timeToEnd <= 100*time.Second

				status := "⏳ WAITING"
				if diffOK && timeOK {
					status = "🟢 READY"
				} else if timeToEnd < 30*time.Second {
					status = "⚠️ TOO LATE"
				} else if timeToEnd > 100*time.Second {
					status = "⏳ WAIT " + formatDuration(timeToEnd-100*time.Second)
				}

				fmt.Printf("\r💰 $%.2f %s | 🎯 $%.2f (%s$%.0f) | ⏱️ %s | %s | W/L: %d/%d | PnL: $%.2f      ",
					price, changeIcon,
					target, diffSign, absDiff,
					formatDuration(timeToEnd),
					status,
					stats.TotalWins, stats.TotalLosses,
					stats.TotalPnL,
				)
			} else {
				fmt.Printf("\r💰 $%.2f %s | 🔍 Waiting for market... | W/L: %d/%d | PnL: $%.2f      ",
					price, changeIcon,
					stats.TotalWins, stats.TotalLosses,
					stats.TotalPnL,
				)
			}
		}
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "ENDED"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

// marketResolutionLoop checks for markets that need resolution.
func marketResolutionLoop(ctx context.Context, engine *simulator.Engine, getCurrentPrice func() float64) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()

			// Check all active markets (not just trades)
			for _, market := range engine.GetActiveMarkets() {
				if now.After(market.EndTime) {
					// Market has ended - resolve it
					engine.ResolveMarket(market.MarketID, getCurrentPrice())
				}
			}

			// Also check pending trades
			for _, trade := range engine.GetTrades() {
				if trade.Outcome == simulator.OutcomePending && now.After(trade.MarketEndTime) {
					engine.ResolveMarket(trade.MarketID, getCurrentPrice())
				}
			}
		}
	}
}

// runDemoMode simulates price data for testing.
func runDemoMode(ctx context.Context, engine *simulator.Engine) {
	basePrice := 95000.0
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Create a demo market
	now := time.Now()
	engine.GetOrCreateMarketState(
		"demo-market-1",
		basePrice,
		now,
		now.Add(5*time.Minute),
	)

	direction := 1.0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Simulate price movement
			change := (float64(time.Now().UnixNano()%100) - 50) * 0.5 * direction
			basePrice += change

			// Occasionally flip direction
			if time.Now().UnixNano()%30 == 0 {
				direction *= -1
			}

			engine.ProcessPriceUpdate(basePrice, time.Now())
		}
	}
}
