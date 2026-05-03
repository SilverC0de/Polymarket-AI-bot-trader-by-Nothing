package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	clobBaseURL      = "https://clob.polymarket.com"
	gammaBaseURL     = "https://gamma-api.polymarket.com"
	priceToBeatURL   = "https://polymarket.com/api/equity/price-to-beat"
)

// Client is the Polymarket API client.
type Client struct {
	httpClient *http.Client
	clobURL    string
	gammaURL   string
	apiKey     string
}

type Option func(*Client)

func WithAPIKey(key string) Option {
	return func(c *Client) {
		c.apiKey = key
	}
}

func WithClobURL(url string) Option {
	return func(c *Client) {
		c.clobURL = url
	}
}

func WithGammaURL(url string) Option {
	return func(c *Client) {
		c.gammaURL = url
	}
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		clobURL:    clobBaseURL,
		gammaURL:   gammaBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ListEvents fetches events matching the given filters.
func (c *Client) ListEvents(ctx context.Context, filters map[string]string) ([]Event, error) {
	u, _ := url.Parse(c.gammaURL + "/events")
	q := u.Query()
	for k, v := range filters {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch events: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var events []Event
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return events, nil
}

// SearchBTC5mEvents searches for active BTC 5-minute markets.
func (c *Client) SearchBTC5mEvents(ctx context.Context) ([]Event, error) {
	// Search for "Bitcoin Up or Down" which matches both
	// "BTC Up or Down 5m" and "Bitcoin Up or Down - May 1, 9:15PM-9:20PM ET"
	events, err := c.ListEvents(ctx, map[string]string{
		"tag_slug":  "bitcoin",
		"active":    "true",
		"closed":    "false",
		"limit":     "50",
		"order":     "endDate",
		"ascending": "true",
	})
	if err != nil {
		// Fallback to title search
		return c.ListEvents(ctx, map[string]string{
			"title_like": "Bitcoin Up or Down",
			"active":     "true",
			"closed":     "false",
			"limit":      "50",
			"order":      "endDate",
			"ascending":  "true",
		})
	}

	// Filter to only 5-minute markets
	var btc5m []Event
	for _, e := range events {
		titleLower := strings.ToLower(e.Title)
		// Match "5m" or time ranges like "9:15PM-9:20PM" (5 min difference)
		if strings.Contains(titleLower, "up or down") &&
			(strings.Contains(titleLower, "5m") ||
				strings.Contains(titleLower, "btc") ||
				strings.Contains(titleLower, "bitcoin")) {
			btc5m = append(btc5m, e)
		}
	}

	if len(btc5m) > 0 {
		return btc5m, nil
	}
	return events, nil
}

// ListSeries fetches series matching filters.
func (c *Client) ListSeries(ctx context.Context, filters map[string]string) ([]Series, error) {
	u, _ := url.Parse(c.gammaURL + "/series")
	q := u.Query()
	for k, v := range filters {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch series: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var series []Series
	if err := json.Unmarshal(body, &series); err != nil {
		return nil, fmt.Errorf("decode series: %w", err)
	}
	return series, nil
}

// GetBTC5mSeries finds the BTC 5-minute Up/Down series.
func (c *Client) GetBTC5mSeries(ctx context.Context) (*Series, error) {
	// Search for BTC 5m series by title instead of recurrence
	series, err := c.ListSeries(ctx, map[string]string{
		"closed": "false",
		"limit":  "100",
	})
	if err != nil {
		return nil, err
	}

	for _, s := range series {
		titleLower := strings.ToLower(s.Title)
		// Match "BTC Up or Down 5m" or similar
		if strings.Contains(titleLower, "btc") &&
			(strings.Contains(titleLower, "5m") || strings.Contains(titleLower, "5 min")) &&
			(strings.Contains(titleLower, "up") || strings.Contains(titleLower, "down")) {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("BTC 5m series not found")
}

// GetSeriesEvents fetches active events for a series.
func (c *Client) GetSeriesEvents(ctx context.Context, seriesID string) ([]Event, error) {
	return c.ListEvents(ctx, map[string]string{
		"series_id": seriesID,
		"active":    "true",
		"closed":    "false",
		"limit":     "10",
		"order":     "startDate",
		"ascending": "true",
	})
}

// GetTags fetches all available tags.
func (c *Client) GetTags(ctx context.Context) ([]Tag, error) {
	u := c.gammaURL + "/tags"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var tags []Tag
	if err := json.Unmarshal(body, &tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	return tags, nil
}

// FindBitcoinTagID finds the Bitcoin tag ID.
func (c *Client) FindBitcoinTagID(ctx context.Context) (string, error) {
	tags, err := c.GetTags(ctx)
	if err != nil {
		return "", err
	}

	for _, t := range tags {
		slugLower := strings.ToLower(t.Slug)
		labelLower := strings.ToLower(t.Label)
		if slugLower == "bitcoin" || labelLower == "bitcoin" {
			return t.ID, nil
		}
	}
	return "", fmt.Errorf("bitcoin tag not found")
}

// SearchActiveBTC5mMarkets fetches BTC 5-minute markets by generating slugs based on current time.
// The slug pattern is: btc-updown-5m-{UNIX_TIMESTAMP} where timestamp is rounded to 5-minute intervals.
func (c *Client) SearchActiveBTC5mMarkets(ctx context.Context) ([]Event, error) {
	now := time.Now().Unix()

	// Generate slugs for current and upcoming 5-minute windows
	var slugs []string
	for i := -1; i <= 3; i++ { // Previous, current, and next 3 windows
		ts := (now/300 + int64(i)) * 300
		slugs = append(slugs, fmt.Sprintf("btc-updown-5m-%d", ts))
	}

	var allEvents []Event
	for _, slug := range slugs {
		events, err := c.ListEvents(ctx, map[string]string{
			"slug": slug,
		})
		if err != nil {
			continue
		}
		allEvents = append(allEvents, events...)
	}

	return allEvents, nil
}

// GetCurrentBTC5mSlug returns the slug for the current 5-minute window.
func GetCurrentBTC5mSlug() string {
	now := time.Now().Unix()
	ts := (now / 300) * 300
	return fmt.Sprintf("btc-updown-5m-%d", ts)
}

// GetNextBTC5mSlug returns the slug for the next 5-minute window.
func GetNextBTC5mSlug() string {
	now := time.Now().Unix()
	ts := ((now / 300) + 1) * 300
	return fmt.Sprintf("btc-updown-5m-%d", ts)
}

// GetEvent fetches a single event by ID.
func (c *Client) GetEvent(ctx context.Context, eventID string) (*Event, error) {
	u := fmt.Sprintf("%s/events/%s", c.gammaURL, eventID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch event: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}
	return &event, nil
}

// GetOrderBook fetches the live order book for a token.
func (c *Client) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	u := fmt.Sprintf("%s/book?token_id=%s", c.clobURL, url.QueryEscape(tokenID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch order book: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var ob OrderBook
	if err := json.Unmarshal(body, &ob); err != nil {
		return nil, fmt.Errorf("decode order book: %w", err)
	}
	return &ob, nil
}

// GetMidpointPrice fetches the midpoint price for a token.
func (c *Client) GetMidpointPrice(ctx context.Context, tokenID string) (float64, error) {
	u := fmt.Sprintf("%s/midpoint?token_id=%s", c.clobURL, url.QueryEscape(tokenID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch midpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Mid string `json:"mid"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode midpoint: %w", err)
	}

	price, err := strconv.ParseFloat(result.Mid, 64)
	if err != nil {
		return 0, fmt.Errorf("parse midpoint: %w", err)
	}
	return price, nil
}

// ParseClobTokenIDs parses the clobTokenIds JSON array string.
func ParseClobTokenIDs(clobTokenIDs string) (yesTokenID, noTokenID string, err error) {
	clobTokenIDs = strings.TrimSpace(clobTokenIDs)
	if clobTokenIDs == "" {
		return "", "", fmt.Errorf("empty clobTokenIds")
	}

	var tokens []string
	if err := json.Unmarshal([]byte(clobTokenIDs), &tokens); err != nil {
		return "", "", fmt.Errorf("parse clobTokenIds: %w", err)
	}

	if len(tokens) < 2 {
		return "", "", fmt.Errorf("expected 2 tokens, got %d", len(tokens))
	}

	return tokens[0], tokens[1], nil
}

// GetFillPrice calculates the actual fill price for a given order size from the order book.
// side is "buy" (we hit asks) or "sell" (we hit bids).
// Returns the volume-weighted average price for filling the given USD amount.
func (ob *OrderBook) GetFillPrice(side string, amountUSD float64) (float64, bool) {
	var levels []BookLevel
	if side == "buy" {
		levels = ob.Asks
	} else {
		levels = ob.Bids
	}

	if len(levels) == 0 {
		return 0, false
	}

	// Calculate VWAP across levels until we fill the order
	var totalShares, totalCost float64
	remaining := amountUSD

	for _, level := range levels {
		if level.Price <= 0 {
			continue
		}
		// Size is in shares, cost per share is price
		levelCost := level.Size * level.Price
		if levelCost >= remaining {
			// This level can fill the rest
			sharesToBuy := remaining / level.Price
			totalShares += sharesToBuy
			totalCost += remaining
			remaining = 0
			break
		}
		// Take entire level
		totalShares += level.Size
		totalCost += levelCost
		remaining -= levelCost
	}

	if totalShares == 0 {
		return 0, false
	}

	// VWAP = total cost / total shares
	vwap := totalCost / totalShares
	
	// If we couldn't fill the entire order, still return what we could get
	filled := remaining == 0
	return vwap, filled
}

// GetBestPrice returns the best available price for a side.
// For "buy", returns best ask. For "sell", returns best bid.
func (ob *OrderBook) GetBestPrice(side string) (float64, bool) {
	if side == "buy" {
		if len(ob.Asks) > 0 && ob.Asks[0].Price > 0 {
			return ob.Asks[0].Price, true
		}
		if ob.BestAsk > 0 {
			return ob.BestAsk, true
		}
	} else {
		if len(ob.Bids) > 0 && ob.Bids[0].Price > 0 {
			return ob.Bids[0].Price, true
		}
		if ob.BestBid > 0 {
			return ob.BestBid, true
		}
	}
	return 0, false
}

// ParseOutcomePrices parses the outcomePrices JSON array string.
func ParseOutcomePrices(prices string) (yesPrice, noPrice float64, err error) {
	prices = strings.TrimSpace(prices)
	if prices == "" {
		return 0.5, 0.5, nil
	}

	var priceList []string
	if err := json.Unmarshal([]byte(prices), &priceList); err != nil {
		return 0, 0, fmt.Errorf("parse outcomePrices: %w", err)
	}

	if len(priceList) < 2 {
		return 0.5, 0.5, nil
	}

	yesPrice, _ = strconv.ParseFloat(priceList[0], 64)
	noPrice, _ = strconv.ParseFloat(priceList[1], 64)
	return yesPrice, noPrice, nil
}

// GetPriceToBeat fetches the official price-to-beat for a market slug.
// This is the Chainlink BTC/USD opening price for BTC 5m markets.
func (c *Client) GetPriceToBeat(ctx context.Context, slug string) (float64, error) {
	u := fmt.Sprintf("%s/%s", priceToBeatURL, url.PathEscape(slug))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch price-to-beat: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Response could be a number or {"price": 77095.20}
	// Try parsing as plain number first
	var price float64
	if err := json.Unmarshal(body, &price); err == nil && price > 0 {
		return price, nil
	}

	// Try parsing as object
	var result struct {
		Price float64 `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode price-to-beat: %w", err)
	}
	return result.Price, nil
}
