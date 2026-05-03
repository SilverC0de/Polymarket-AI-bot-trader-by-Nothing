package service

import (
	"context"
	"fmt"

	"github.com/silver/pmvibes/pkg/polymarket"
)

type FinanceService struct {
	pmClient *polymarket.Client
}

func NewFinanceService(pmClient *polymarket.Client) *FinanceService {
	return &FinanceService{pmClient: pmClient}
}

// Position represents an open trade position.
type Position struct {
	MarketID  string  `json:"market_id"`
	Side      string  `json:"side"`   // "YES" or "NO"
	Size      float64 `json:"size"`   // shares held
	AvgPrice  float64 `json:"avg_price"`
	PnL       float64 `json:"pnl"`
}

// Prediction represents a 5-minute Polymarket prediction signal.
type Prediction struct {
	MarketID    string  `json:"market_id"`
	Question    string  `json:"question"`
	YesPrice    float64 `json:"yes_price"`
	NoPrice     float64 `json:"no_price"`
	Signal      string  `json:"signal"`     // "BUY_YES", "BUY_NO", "HOLD"
	Confidence  float64 `json:"confidence"` // 0.0 – 1.0
}

// TradeRequest is the payload for placing a trade.
type TradeRequest struct {
	MarketID string  `json:"market_id"`
	Side     string  `json:"side"`   // "YES" or "NO"
	Size     float64 `json:"size"`   // USDC amount
}

// TradeResult is returned after a trade is submitted.
type TradeResult struct {
	OrderID  string  `json:"order_id"`
	MarketID string  `json:"market_id"`
	Side     string  `json:"side"`
	Size     float64 `json:"size"`
	Price    float64 `json:"price"`
	Status   string  `json:"status"`
}

func (s *FinanceService) GetOpenPositions(ctx context.Context) ([]Position, error) {
	// TODO: fetch real positions from Polymarket via s.pmClient
	_ = s.pmClient
	return []Position{}, nil
}

func (s *FinanceService) GetPredictions(ctx context.Context, market string) (*Prediction, error) {
	// TODO: call prediction model and fetch live odds from s.pmClient
	_ = s.pmClient
	return &Prediction{
		MarketID:   market,
		Question:   fmt.Sprintf("Stub prediction for market %s", market),
		YesPrice:   0.5,
		NoPrice:    0.5,
		Signal:     "HOLD",
		Confidence: 0.0,
	}, nil
}

func (s *FinanceService) ExecuteTrade(ctx context.Context, req TradeRequest) (*TradeResult, error) {
	if req.MarketID == "" {
		return nil, fmt.Errorf("market_id is required")
	}
	if req.Side != "YES" && req.Side != "NO" {
		return nil, fmt.Errorf("side must be YES or NO")
	}
	if req.Size <= 0 {
		return nil, fmt.Errorf("size must be greater than 0")
	}

	// TODO: submit order via s.pmClient
	_ = s.pmClient
	return &TradeResult{
		OrderID:  "stub-order-id",
		MarketID: req.MarketID,
		Side:     req.Side,
		Size:     req.Size,
		Price:    0.5,
		Status:   "pending",
	}, nil
}
