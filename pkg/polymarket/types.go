package polymarket

import "time"

// Tag represents a Polymarket tag/category.
type Tag struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Slug  string `json:"slug"`
}

// Series represents a recurring Polymarket series (like BTC 5m).
type Series struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Recurrence  string    `json:"recurrence"` // e.g., "daily", "weekly"
	Events      []Event   `json:"events,omitempty"`
	Closed      bool      `json:"closed"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Event represents a Polymarket event containing multiple markets.
type Event struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	StartDate       time.Time `json:"startDate"`
	EndDate         time.Time `json:"endDate"`
	Markets         []Market  `json:"markets"`
	Active          bool      `json:"active"`
	Closed          bool      `json:"closed"`
	SeriesID        string    `json:"seriesId,omitempty"`
	EnableOrderBook bool      `json:"enableOrderBook"`
}

// Market represents a single Polymarket prediction market.
type Market struct {
	ID               string    `json:"id"`
	ConditionID      string    `json:"condition_id"`
	Slug             string    `json:"slug"`
	Question         string    `json:"question"`
	Description      string    `json:"description"`
	EndDate          time.Time `json:"endDate"`
	Active           bool      `json:"active"`
	Closed           bool      `json:"closed"`
	ClobTokenIDs     string    `json:"clobTokenIds"`
	Volume           string    `json:"volume"`
	VolumeNum        float64   `json:"volumeNum"`
	OutcomePrices    string    `json:"outcomePrices"`
	Outcomes         string    `json:"outcomes"`        // JSON array: ["Up", "Down"]
	BestBid          float64   `json:"bestBid"`
	BestAsk          float64   `json:"bestAsk"`
	LastTradePrice   float64   `json:"lastTradePrice"`
}

// BTC5mMarket represents a parsed BTC 5-minute prediction market.
type BTC5mMarket struct {
	MarketID     string
	EventID      string
	Question     string
	StartTime    time.Time
	EndTime      time.Time
	PriceToBeat  float64 // BTC price at start
	YesTokenID   string  // "Up" token
	NoTokenID    string  // "Down" token
	YesPrice     float64 // Current price of "Up"
	NoPrice      float64 // Current price of "Down"
	Resolved     bool
	WinningToken string // "YES" or "NO" after resolution
}

// OrderBook represents the live order book for a token.
type OrderBook struct {
	TokenID   string      `json:"token_id"`
	Bids      []BookLevel `json:"bids"`
	Asks      []BookLevel `json:"asks"`
	BestBid   float64     `json:"best_bid"`
	BestAsk   float64     `json:"best_ask"`
	Spread    float64     `json:"spread"`
	LastPrice float64     `json:"last_price"`
}

// BookLevel represents a price level in the order book.
type BookLevel struct {
	Price float64 `json:"price,string"`
	Size  float64 `json:"size,string"`
}

// PriceUpdate from WebSocket stream.
type PriceUpdate struct {
	Symbol    string  `json:"symbol"`
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// EventsResponse from the Gamma API.
type EventsResponse struct {
	Events     []Event `json:"data"`
	NextCursor string  `json:"next_cursor"`
}

// MarketsResponse from the Gamma API.
type MarketsResponse struct {
	Markets    []Market `json:"data"`
	NextCursor string   `json:"next_cursor"`
}
