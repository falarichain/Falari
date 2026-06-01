package chain

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

// wsEvents handles WebSocket connections for real-time event streaming.
// Clients can specify the same filter query parameters as GET /events
// to receive only matching events.
func (s *Server) wsEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Parse filter from query parameters.
	q := r.URL.Query()
	f := EventFilter{
		Type:            q.Get("type"),
		Address:         q.Get("address"),
		IntentID:        q.Get("intent_id"),
		Counterparty:    q.Get("counterparty"),
		TransactionHash: q.Get("tx_hash"),
	}
	if v := q.Get("min_height"); v != "" {
		f.MinHeight, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("max_height"); v != "" {
		f.MaxHeight, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("block_height"); v != "" {
		f.ExactHeight, _ = strconv.ParseInt(v, 10, 64)
	}

	// Subscribe to the event bus.
	if s.store.eventBus == nil {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "event bus not available"))
		return
	}
	ch := s.store.eventBus.Subscribe()
	defer s.store.eventBus.Unsubscribe(ch)

	// Set up ping/pong for keepalive.
	const pingPeriod = 30 * time.Second
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pingPeriod * 2))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(pingPeriod * 2))

	// Start a goroutine to read client messages (for close handling).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Stream events to the client.
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if !eventMatchesFilter(evt, f) {
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
