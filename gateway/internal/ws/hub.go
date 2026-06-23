// Package ws implements a WebSocket fan-out hub: one goroutine owns all client
// state, and clients are registered/unregistered/broadcast-to over channels.
//
// WHY this design:
//   - A single "hub" goroutine is the *only* writer of the clients map. That
//     removes the need for a mutex on the hot broadcast path and makes the
//     concurrency trivially correct (the Go memory model guarantees the map is
//     never touched by two goroutines at once). This mirrors the engine's
//     single-writer-per-symbol philosophy.
//   - Each client has a buffered `send` channel. The hub does a NON-BLOCKING
//     send. If a client is too slow to drain its buffer (backpressure), we drop
//     and disconnect *that* client rather than letting one slow consumer stall
//     the entire fan-out. A market-data feed must never block on a laggard.
package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/itsharsh007/openexchange/gateway/internal/metrics"
)

// sendBuffer is how many messages may queue per client before we consider it
// too slow and drop it. Sized for short bursts, not for a stalled client.
const sendBuffer = 64

const (
	writeWait  = 10 * time.Second    // max time allowed to write a frame
	pongWait   = 60 * time.Second    // max time we wait for a pong
	pingPeriod = (pongWait * 9) / 10 // must be < pongWait
)

// upgrader turns an HTTP connection into a WebSocket. CheckOrigin returns true
// here for simplicity (dev). In production restrict this to the dashboard's
// origin to prevent cross-site WebSocket hijacking.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client is a single connected WebSocket peer.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte // buffered outbound queue; hub writes, writePump drains
}

// Hub maintains the set of active clients and broadcasts messages to them.
// All fields are touched only by Run's goroutine (except the channels, which
// are the synchronization mechanism).
type Hub struct {
	clients    map[*Client]struct{}
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	countReq   chan chan int // ask the hub goroutine for the live client count
}

// NewHub allocates a hub. Call Run in a goroutine before using it.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		countReq:   make(chan chan int),
	}
}

// Run is the hub's event loop. It is the single owner of the clients map.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = struct{}{}
			metrics.WSClientsGauge.Set(float64(len(h.clients)))
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send) // signals writePump to exit
				metrics.WSClientsGauge.Set(float64(len(h.clients)))
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
					// delivered (or queued in the client's buffer)
				default:
					// Backpressure: client buffer full -> it's too slow.
					// Drop it instead of blocking every other client.
					delete(h.clients, c)
					close(c.send)
				}
			}
		case reply := <-h.countReq:
			reply <- len(h.clients)
		}
	}
}

// Broadcast marshals v to JSON and queues it for all clients. Safe to call from
// any goroutine; the actual fan-out happens on the hub goroutine.
func (h *Hub) Broadcast(v any) {
	msg, err := json.Marshal(v)
	if err != nil {
		log.Printf("ws: marshal broadcast: %v", err)
		return
	}
	// Non-blocking so a backed-up broadcast channel never stalls a producer
	// (e.g. the trade ingest path). If the channel is full we drop the message.
	select {
	case h.broadcast <- msg:
	default:
		log.Printf("ws: broadcast channel full, dropping message")
	}
}

// ServeWS upgrades an HTTP request to a WebSocket and registers the client.
// Intended to be used as an http.HandlerFunc (after auth middleware).
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade failed: %v", err)
		return
	}
	client := &Client{hub: h, conn: conn, send: make(chan []byte, sendBuffer)}
	h.register <- client

	// Two goroutines per connection: one reads (mostly to detect close/pongs),
	// one writes. This is the idiomatic gorilla/websocket pattern because a
	// connection must have at most one concurrent reader and one writer.
	go client.writePump()
	go client.readPump()
}

// readPump drains inbound messages. The hub does not act on client input in
// this fan-out-only design, but we MUST keep reading to process control frames
// (ping/pong/close) and to detect a dead peer.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(512) // clients shouldn't send us large payloads
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break // peer closed or timed out
		}
	}
}

// writePump pulls from the client's send channel and writes frames, and sends
// periodic pings to keep the connection alive and detect half-open sockets.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// hub closed the channel -> graceful close handshake
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ClientCount reports the current number of connected clients. It asks the hub
// goroutine (the sole owner of the clients map) over a request channel, so it
// is race-free even though the map itself is unsynchronized.
func (h *Hub) ClientCount() int {
	reply := make(chan int)
	h.countReq <- reply
	return <-reply
}
