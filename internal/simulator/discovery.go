package simulator

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silver/pmvibes/pkg/polymarket"
)

// DiscoveredMarket contains info about a discovered BTC 5m market.
type DiscoveredMarket struct {
	MarketID    string
	EventTitle  string
	PriceToBeat float64
	StartTime   time.Time
	EndTime     time.Time
}

// MarketDiscoverer finds active BTC 5m markets and captures accurate price targets.
type MarketDiscoverer struct {
	client         *polymarket.Client
	mu             sync.RWMutex
	capturedPrices map[string]float64  // marketID -> captured price
	boundaryPrices map[int64]float64   // unix timestamp (5-min aligned) -> BTC price at that boundary
}

// NewMarketDiscoverer creates a new market discoverer.
func NewMarketDiscoverer(client *polymarket.Client) *MarketDiscoverer {
	return &MarketDiscoverer{
		client:         client,
		capturedPrices: make(map[string]float64),
		boundaryPrices: make(map[int64]float64),
	}
}

// RecordPrice records the BTC price and captures it if we're at a 5-minute boundary.
// Call this on every price update to capture accurate boundary prices.
func (d *MarketDiscoverer) RecordPrice(price float64, timestamp time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if this timestamp is within 1 second of a 5-minute boundary
	unix := timestamp.Unix()
	aligned := (unix / 300) * 300
	diff := unix - aligned

	// If within 1 second of a 5-min mark, capture it
	if diff <= 1 {
		if _, exists := d.boundaryPrices[aligned]; !exists {
			d.boundaryPrices[aligned] = price
		}
	} else if diff >= 299 {
		// Very close to next boundary, round up
		nextBoundary := aligned + 300
		if _, exists := d.boundaryPrices[nextBoundary]; !exists {
			d.boundaryPrices[nextBoundary] = price
		}
	}

	// Clean up old boundary prices (older than 15 minutes)
	cutoff := aligned - 900
	for ts := range d.boundaryPrices {
		if ts < cutoff {
			delete(d.boundaryPrices, ts)
		}
	}
}

// GetBoundaryPrice returns the captured price at a specific 5-minute boundary.
func (d *MarketDiscoverer) GetBoundaryPrice(boundaryTS int64) (float64, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	price, ok := d.boundaryPrices[boundaryTS]
	return price, ok
}

// DiscoverMarkets finds active BTC 5m markets and returns them with captured price targets.
// It uses the boundary price captured at the window start time as the target.
func (d *MarketDiscoverer) DiscoverMarkets(ctx context.Context, currentBTCPrice float64) ([]DiscoveredMarket, error) {
	events, err := d.client.SearchActiveBTC5mMarkets(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	var results []DiscoveredMarket

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, event := range events {
		titleLower := strings.ToLower(event.Title)
		if !strings.Contains(titleLower, "up or down") {
			continue
		}
		if !strings.Contains(titleLower, "btc") && !strings.Contains(titleLower, "bitcoin") {
			continue
		}

		// Skip events that already ended
		if !event.EndDate.IsZero() && event.EndDate.Before(now) {
			continue
		}

		// Skip events ending more than 6 minutes from now
		if !event.EndDate.IsZero() && event.EndDate.After(now.Add(6*time.Minute)) {
			continue
		}

		for _, market := range event.Markets {
			if market.Closed {
				continue
			}

			// Parse window start time from slug (btc-updown-5m-{unix_timestamp})
			var windowStartTS int64
			if strings.HasPrefix(market.Slug, "btc-updown-5m-") {
				tsStr := strings.TrimPrefix(market.Slug, "btc-updown-5m-")
				if ts, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
					windowStartTS = ts
				}
			}

			if windowStartTS == 0 {
				continue
			}

			windowStart := time.Unix(windowStartTS, 0)
			endTime := windowStart.Add(5 * time.Minute)

			// Check if we already have a captured price for this market
			if capturedPrice, ok := d.capturedPrices[market.ID]; ok {
				results = append(results, DiscoveredMarket{
					MarketID:    market.ID,
					EventTitle:  event.Title,
					PriceToBeat: capturedPrice,
					StartTime:   windowStart,
					EndTime:     endTime,
				})
				continue
			}

			// Try to get the boundary price for this window's start time
			if boundaryPrice, ok := d.boundaryPrices[windowStartTS]; ok {
				// We have the exact price at window start!
				d.capturedPrices[market.ID] = boundaryPrice
				results = append(results, DiscoveredMarket{
					MarketID:    market.ID,
					EventTitle:  event.Title,
					PriceToBeat: boundaryPrice,
					StartTime:   windowStart,
					EndTime:     endTime,
				})
				continue
			}

			// Window has started but we don't have the boundary price
			// Use current price as fallback (will be slightly off)
			timeSinceStart := now.Sub(windowStart)
			if timeSinceStart > 0 {
				d.capturedPrices[market.ID] = currentBTCPrice
				results = append(results, DiscoveredMarket{
					MarketID:    market.ID,
					EventTitle:  event.Title,
					PriceToBeat: currentBTCPrice,
					StartTime:   windowStart,
					EndTime:     endTime,
				})
			}
			// If window hasn't started yet, skip for now - we'll catch it on next poll
		}
	}

	return results, nil
}
