package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	rtdsURL = "wss://ws-live-data.polymarket.com"

	// readTimeout is the maximum time we'll wait between messages from the server
	// before considering the connection dead. The server should send updates much
	// more frequently than this; if it doesn't, the TCP connection is likely
	// half-open and we want to force a reconnect.
	readTimeout = 30 * time.Second

	// pingInterval controls how often we send keepalive PINGs.
	pingInterval = 5 * time.Second

	// reconnect backoff bounds.
	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
)

// WSMessage represents a WebSocket message structure.
type WSMessage struct {
	Topic     string          `json:"topic"`
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// WSSubscription represents a subscription request.
type WSSubscription struct {
	Topic   string `json:"topic"`
	Type    string `json:"type"`
	Filters string `json:"filters,omitempty"`
}

// WSRequest represents a WebSocket request.
type WSRequest struct {
	Action        string           `json:"action"`
	Subscriptions []WSSubscription `json:"subscriptions"`
}

// PriceHandler is called when a new BTC price update arrives.
type PriceHandler func(price PriceUpdate)

// PriceClient streams real-time BTC prices from Polymarket with auto-reconnect.
type PriceClient struct {
	handler PriceHandler
	logger  *slog.Logger

	mu          sync.RWMutex
	conn        *websocket.Conn
	lastPrice   PriceUpdate
	lastMsgAt   time.Time
	connected   bool

	supervisorCtx    context.Context
	supervisorCancel context.CancelFunc
	supervisorDone   chan struct{}
}

// NewPriceClient creates a new real-time price streaming client.
func NewPriceClient(handler PriceHandler, logger *slog.Logger) *PriceClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &PriceClient{
		handler: handler,
		logger:  logger,
	}
}

// Connect performs an initial dial+subscribe and starts a background supervisor
// that auto-reconnects (with exponential backoff) if the connection drops or
// the read deadline is exceeded. The first dial is synchronous so the caller
// gets immediate feedback if the endpoint is unreachable; later disconnects are
// handled transparently by the supervisor.
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

// dialAndSubscribe opens a fresh websocket and sends the subscription request.
func (pc *PriceClient) dialAndSubscribe(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}

	conn, _, err := dialer.DialContext(ctx, rtdsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial websocket: %w", err)
	}

	subReq := WSRequest{
		Action: "subscribe",
		Subscriptions: []WSSubscription{
			{
				Topic:   "crypto_prices_chainlink",
				Type:    "*",
				Filters: `{"symbol":"btc/usd"}`,
			},
		},
	}
	if err := conn.WriteJSON(subReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send subscription: %w", err)
	}

	// Force the read loop to wake up if the server stops sending traffic.
	// Bumped on every successful read.
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	pc.logger.Info("connected to Polymarket RTDS, subscribed to btc/usd")
	return conn, nil
}

// supervise runs the read+ping loops for a single connection. When that
// connection dies for any reason it redials with exponential backoff until
// the supervisor context is canceled (via Close).
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
		// Run the read+ping loops for this connection until one of them errors.
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

		pc.logger.Warn("price stream disconnected, reconnecting", "backoff", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		newConn, err := pc.dialAndSubscribe(ctx)
		if err != nil {
			pc.logger.Error("price stream reconnect failed", "err", err, "next_backoff", backoff)
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

// runConn drives one connection's read + ping goroutines and returns when
// either of them exits (which signals the caller to reconnect).
func (pc *PriceClient) runConn(ctx context.Context, conn *websocket.Conn) {
	connDone := make(chan struct{})
	go pc.readLoop(conn, connDone)
	go pc.pingLoop(ctx, conn, connDone)

	select {
	case <-connDone:
	case <-ctx.Done():
	}
}

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
				pc.logger.Info("websocket closed normally")
			} else {
				pc.logger.Error("read error", "err", err)
			}
			return
		}

		// Any message — even a PONG — counts as liveness; bump the deadline.
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		pc.mu.Lock()
		pc.lastMsgAt = time.Now()
		pc.mu.Unlock()

		msgStr := string(message)
		if len(message) == 0 || msgStr == "PONG" {
			continue
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			pc.logger.Debug("ignoring non-JSON message", "raw", msgStr)
			continue
		}

		if msg.Topic == "crypto_prices_chainlink" && msg.Type == "update" {
			var price PriceUpdate
			if err := json.Unmarshal(msg.Payload, &price); err != nil {
				pc.logger.Warn("unmarshal price", "err", err)
				continue
			}

			pc.mu.Lock()
			pc.lastPrice = price
			pc.mu.Unlock()

			if pc.handler != nil {
				pc.handler(price)
			}
		}
	}
}

func (pc *PriceClient) pingLoop(ctx context.Context, conn *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.TextMessage, []byte("PING")); err != nil {
				pc.logger.Error("ping failed", "err", err)
				// Force the read side to wake up immediately so the supervisor
				// can reconnect rather than waiting out the read deadline.
				_ = conn.SetReadDeadline(time.Now())
				return
			}
		}
	}
}

// LastPrice returns the most recent BTC price.
func (pc *PriceClient) LastPrice() PriceUpdate {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastPrice
}

// LastMessageAt returns the timestamp of the most recent inbound message
// (any message, including pongs). Zero value means no message has been
// received yet.
func (pc *PriceClient) LastMessageAt() time.Time {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.lastMsgAt
}

// Connected reports whether the client currently believes it has a live
// websocket connection. This does not by itself guarantee fresh data;
// callers that care about staleness should also check LastMessageAt.
func (pc *PriceClient) Connected() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.connected
}

// Close stops the supervisor and tears down the current connection.
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
