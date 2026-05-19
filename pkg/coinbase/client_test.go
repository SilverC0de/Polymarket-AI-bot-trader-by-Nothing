package coinbase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPriceClient_ConnectAndReceive(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	receivedChan := make(chan PriceUpdate, 1)

	// Set up mock Coinbase Exchange WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade server connection: %v", err)
			return
		}
		defer conn.Close()

		// Read subscription message
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("Failed to read subscription: %v", err)
			return
		}

		var sub Subscription
		if err := json.Unmarshal(msg, &sub); err != nil {
			t.Errorf("Failed to unmarshal subscription message: %v", err)
			return
		}

		if sub.Type != "subscribe" || len(sub.ProductIDs) == 0 || sub.ProductIDs[0] != "BTC-USD" {
			t.Errorf("Unexpected subscription: %+v", sub)
			return
		}

		// Send mock ticker price message
		ticker := TickerMessage{
			Type:      "ticker",
			ProductID: "BTC-USD",
			Price:     "67890.20",
			Time:      "2026-05-19T08:22:31.123456Z",
		}
		data, err := json.Marshal(ticker)
		if err != nil {
			t.Errorf("Failed to marshal mock ticker: %v", err)
			return
		}

		err = conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			t.Errorf("Failed to send ticker to client: %v", err)
			return
		}

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http", "ws", 1)

	client := NewPriceClient(func(price PriceUpdate) {
		receivedChan <- price
	}, nil)
	client.wsURL = wsURL

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	defer client.Close()

	select {
	case price := <-receivedChan:
		if price.Symbol != "BTC-USD" {
			t.Errorf("Expected product BTC-USD, got %s", price.Symbol)
		}
		if price.Value != 67890.20 {
			t.Errorf("Expected value 67890.20, got %f", price.Value)
		}
		expectedTime, _ := time.Parse(time.RFC3339Nano, "2026-05-19T08:22:31.123456Z")
		if !price.Timestamp.Equal(expectedTime) {
			t.Errorf("Expected timestamp %v, got %v", expectedTime, price.Timestamp)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for price update from mock server")
	}

	if !client.Connected() {
		t.Error("Expected client to report Connected() as true")
	}
}
