package coinbase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// coinbaseWSURL is the public WebSocket feed for Coinbase Exchange
	coinbaseWSURL = "wss://ws-feed.exchange.coinbase.com"

	// readTimeout is the maximum time we'll wait between messages from Coinbase
	// before considering the connection dead. Since BTC-USD trades occur
	// multiple times per second, 15 seconds is very generous.
	readTimeout = 15 * time.Second

	// reconnect backoff bounds
	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
)

// PriceUpdate represents a parsed BTC price update from Coinbase.
type PriceUpdate struct {
	Symbol    string    `json:"symbol"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// Subscription represents the subscription request message for Coinbase.
type Subscription struct {
	Type       string   `json:"type"`
	ProductIDs []string `json:"product_ids"`
	Channels   []string `json:"channels"`
}

// TickerMessage represents the ticker message format from Coinbase feed.
type TickerMessage struct {
	Type      string `json:"type"`
	ProductID string `json:"product_id"`
	Price     string `json:"price"`
	Time      string `json:"time"`
}

// PriceHandler is the callback function for price updates.
type PriceHandler func(price PriceUpdate)

// PriceClient streams real-time BTC prices from Coinbase with auto-reconnect.
type PriceClient struct {
	handler PriceHandler
	logger  *slog.Logger
	wsURL   string

	mu        sync.RWMutex
	conn      *websocket.Conn
	lastPrice PriceUpdate
	lastMsgAt time.Time
	connected bool

	supervisorCtx    context.Context
	supervisorCancel context.CancelFunc
	supervisorDone   chan struct{}
}

// NewPriceClient creates a new real-time Coinbase price streaming client.
func NewPriceClient(handler PriceHandler, logger *slog.Logger) *PriceClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &PriceClient{
		handler: handler,
		logger:  logger,
	}
}

// Connect performs the initial connection and starts the supervisor.
func (pc *PriceClient) Connect(ctx context.Context) error {
	conn, err := pc.dialAndSubscribe(ctx)
	if err != nil {
		return err
	}

	pc.mu.Lock()
	pc.conn = conn
	pc.connected = true
	pc.lastMsgAt = time.Now()
	supCtx, cancel := context.WithCancel(context.Background())
	pc.supervisorCtx = supCtx
	pc.supervisorCancel = cancel
	pc.supervisorDone = make(chan struct{})
	pc.mu.Unlock()

	go pc.supervise(supCtx, conn)
	return nil
}

// dialAndSubscribe opens a fresh WebSocket connection to Coinbase and sends the subscription message.
func (pc *PriceClient) dialAndSubscribe(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	url := coinbaseWSURL
	if pc.wsURL != "" {
		url = pc.wsURL
	}

	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial Coinbase websocket (%s): %w", url, err)
	}

	// Subscribe to ticker channel for BTC-USD
	sub := Subscription{
		Type:       "subscribe",
		ProductIDs: []string{"BTC-USD"},
		Channels:   []string{"ticker"},
	}

	if err := conn.WriteJSON(sub); err != nil {
		conn.Close()
		return nil, fmt.Errorf("coinbase subscribe: %w", err)
	}

	// Set initial read deadline
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	pc.logger.Info("connected to Coinbase public Exchange stream (BTC-USD)")
	return conn, nil
}

// supervise handles disconnection and reconnection.
func (pc *PriceClient) supervise(ctx context.Context, initial *websocket.Conn) {
	defer func() {
		pc.mu.Lock()
		done := pc.supervisorDone
		pc.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()

	conn := initial
	backoff := minBackoff

	for {
		pc.runConn(ctx, conn)

		pc.mu.Lock()
		pc.connected = false
		if pc.conn == conn {
			pc.conn = nil
		}
		pc.mu.Unlock()
		_ = conn.Close()

		if ctx.Err() != nil {
			return
		}

		pc.logger.Warn("Coinbase price stream disconnected, reconnecting", "backoff", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		newConn, err := pc.dialAndSubscribe(ctx)
		if err != nil {
			pc.logger.Error("Coinbase price stream reconnect failed", "err", err, "next_backoff", backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		pc.mu.Lock()
		pc.conn = newConn
		pc.connected = true
		pc.lastMsgAt = time.Now()
		pc.mu.Unlock()

		conn = newConn
		backoff = minBackoff
	}
}

// runConn runs the read loop.
func (pc *PriceClient) runConn(ctx context.Context, conn *websocket.Conn) {
	connDone := make(chan struct{})
	go pc.readLoop(conn, connDone)

	select {
	case <-connDone:
	case <-ctx.Done():
	}
}

// readLoop reads messages from the WebSocket.
func (pc *PriceClient) readLoop(conn *websocket.Conn, done chan struct{}) {
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				pc.logger.Info("Coinbase websocket closed normally")
			} else {
				pc.logger.Error("Coinbase read error", "err", err)
			}
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		pc.mu.Lock()
		pc.lastMsgAt = time.Now()
		pc.mu.Unlock()

		if len(message) == 0 {
			continue
		}

		var raw TickerMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			pc.logger.Debug("ignoring non-ticker message or parse error", "err", err)
			continue
		}

		if raw.Type == "ticker" && raw.ProductID == "BTC-USD" && raw.Price != "" {
			val, err := strconv.ParseFloat(raw.Price, 64)
			if err != nil {
				pc.logger.Warn("failed to parse Coinbase price", "price", raw.Price, "err", err)
				continue
			}

			var timestamp time.Time
			if raw.Time != "" {
				if t, err := time.Parse(time.RFC3339Nano, raw.Time); err == nil {
					timestamp = t
				}
			}
			if timestamp.IsZero() {
				timestamp = time.Now()
			}

			priceUpdate := PriceUpdate{
				Symbol:    raw.ProductID,
				Timestamp: timestamp,
				Value:     val,
			}

			pc.mu.Lock()
			pc.lastPrice = priceUpdate
			pc.mu.Unlock()

			if pc.handler != nil {
				pc.handler(priceUpdate)
			}
		}
	}
}

// LastPrice returns the most recent price update.
func (pc *PriceClient) LastPrice() PriceUpdate {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastPrice
}

// LastMessageAt returns the timestamp of the last message received.
func (pc *PriceClient) LastMessageAt() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastMsgAt
}

// Connected reports connection status.
func (pc *PriceClient) Connected() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.connected
}

// Close stops the supervisor and closes connection.
func (pc *PriceClient) Close() error {
	pc.mu.Lock()
	cancel := pc.supervisorCancel
	conn := pc.conn
	done := pc.supervisorDone
	pc.supervisorCancel = nil
	pc.conn = nil
	pc.connected = false
	pc.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var err error
	if conn != nil {
		_ = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		err = conn.Close()
	}

	if done != nil {
		<-done
	}
	return err
}
