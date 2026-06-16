package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestHubBroadcast spins up the hub behind an httptest server, dials a real
// WebSocket client, broadcasts a message, and asserts the client receives it.
func TestHubBroadcast(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Give the hub a moment to register the client (register is async).
	waitFor(t, func() bool { return hub.ClientCount() == 1 })

	hub.Broadcast(map[string]string{"type": "trade", "symbol": "AAPL"})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "AAPL") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

// TestHubDropsSlowClient verifies backpressure handling: a client whose buffer
// is full gets unregistered rather than blocking the hub.
func TestHubDropsSlowClient(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	// Register a client manually with a tiny buffer and never drain it.
	slow := &Client{hub: hub, send: make(chan []byte, 1)}
	hub.register <- slow
	waitFor(t, func() bool { return hub.ClientCount() == 1 })

	// Flood: first fills the buffer, subsequent ones trip the default branch
	// and cause the hub to drop the client.
	for i := 0; i < 5; i++ {
		hub.Broadcast(map[string]int{"n": i})
		time.Sleep(5 * time.Millisecond)
	}

	waitFor(t, func() bool { return hub.ClientCount() == 0 })
}

// waitFor polls cond up to ~1s. Used because hub register/broadcast are async.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
