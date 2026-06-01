package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WSClient struct {
	Conn      *websocket.Conn
	SessionID string
	Send      chan []byte
	mu        sync.Mutex
}

func (c *WSClient) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteJSON(v)
}

func (c *WSClient) WriteMessage(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteMessage(websocket.TextMessage, data)
}

type WSHub struct {
	mu      sync.RWMutex
	clients map[*WSClient]bool
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients: make(map[*WSClient]bool),
	}
}

func (h *WSHub) Register(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = true
}

func (h *WSHub) Unregister(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.Send)
	}
}

func (h *WSHub) Broadcast(envelope *MessageEnvelope) {
	data, err := envelope.Marshal()
	if err != nil {
		log.Printf("error marshaling broadcast: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.Send <- data:
		default:
			go h.Unregister(client)
		}
	}
}

func (h *WSHub) BroadcastJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("error marshaling broadcast json: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.Send <- data:
		default:
			go h.Unregister(client)
		}
	}
}

func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
