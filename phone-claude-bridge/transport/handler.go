package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"phone-claude-bridge/auth"
	"phone-claude-bridge/session"
	"phone-claude-bridge/store"
)

type Handler struct {
	sm       *session.SessionManager
	store    *store.Store
	auth     *auth.AuthHandler
	sessions map[string]*WSHub
}

func NewHandler(sm *session.SessionManager, st *store.Store, ah *auth.AuthHandler) *Handler {
	return &Handler{
		sm:       sm,
		store:    st,
		auth:     ah,
		sessions: make(map[string]*WSHub),
	}
}

func (h *Handler) GetOrCreateHub(sessionID string) *WSHub {
	if hub, ok := h.sessions[sessionID]; ok {
		return hub
	}
	hub := NewWSHub()
	h.sessions[sessionID] = hub
	return hub
}

func (h *Handler) RemoveHub(sessionID string) {
	delete(h.sessions, sessionID)
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", h.handleHealth)
	mux.HandleFunc("/api/v1/auth/pair", h.auth.HandlePair)
	mux.HandleFunc("/api/v1/auth/verify", h.auth.HandleVerify)
	mux.HandleFunc("/api/v1/auth/refresh", h.auth.HandleRefresh)
	mux.HandleFunc("/api/v1/sessions", h.handleSessions)
	mux.HandleFunc("/api/v1/sessions/", h.handleSessionByID)
	mux.HandleFunc("/api/v1/fs/", h.handleFS)
	mux.HandleFunc("/ws", h.handleWebSocket)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "ok",
		"sessions": h.sm.Count(),
	})
}

func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions := h.sm.List()
		type SessionInfo struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			WorkDir    string `json:"work_dir"`
			Status     string `json:"status"`
			LastActive int64  `json:"last_active"`
		}
		result := make([]SessionInfo, 0, len(sessions))
		for _, s := range sessions {
			result = append(result, SessionInfo{
				ID:         s.ID,
				Name:       s.Name,
				WorkDir:    s.WorkDir,
				Status:     string(s.GetStatus()),
				LastActive: s.LastActive.Unix(),
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"sessions": result})

	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			WorkDir string `json:"work_dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			req.Name = "default"
		}
		sess, err := h.sm.Create(req.Name, req.WorkDir)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": sess.ID, "name": sess.Name})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, `{"error":"missing session id"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && subPath == "history":
		msgs, err := h.store.GetMessages(sessionID, 200)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"messages": msgs})

	case r.Method == http.MethodGet && subPath == "":
		sess, err := h.sm.Get(sessionID)
		if err != nil {
			http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":          sess.ID,
			"name":        sess.Name,
			"work_dir":    sess.WorkDir,
			"status":      string(sess.GetStatus()),
			"last_active": sess.LastActive.Unix(),
		})

	case r.Method == http.MethodDelete:
		if err := h.sm.Destroy(sessionID); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		h.RemoveHub(sessionID)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleFS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"entries": []interface{}{}})
}

func (h *Handler) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, `{"error":"missing session_id"}`, http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	client := &WSClient{
		Conn:      conn,
		SessionID: sessionID,
		Send:      make(chan []byte, 256),
	}

	hub := h.GetOrCreateHub(sessionID)
	hub.Register(client)

	defer func() {
		hub.Unregister(client)
		if hub.ClientCount() == 0 {
			h.RemoveHub(sessionID)
		}
	}()

	go func() {
		for msg := range client.Send {
			if err := client.WriteMessage(msg); err != nil {
				log.Printf("ws write error: %v", err)
				return
			}
		}
	}()

	h.readPump(client)
}

func (h *Handler) readPump(client *WSClient) {
	defer client.Conn.Close()

	for {
		_, data, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read error: %v", err)
			}
			break
		}

		envelope, err := UnmarshalEnvelope(data)
		if err != nil {
			log.Printf("ws unmarshal error: %v", err)
			continue
		}

		h.handleWSMessage(client, envelope)
	}
}

func (h *Handler) handleWSMessage(client *WSClient, env *MessageEnvelope) {
	switch env.Type {
	case "chat":
		h.handleChatMessage(client, env)
	case "control":
		h.handleControlMessage(client, env)
	case "system":
		h.handleSystemMessage(client, env)
	default:
		log.Printf("unknown message type: %s", env.Type)
	}
}

func (h *Handler) handleChatMessage(client *WSClient, env *MessageEnvelope) {
	var payload ChatPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("chat payload unmarshal error: %v", err)
		return
	}

	sess, err := h.sm.Get(payload.SessionID)
	if err != nil {
		log.Printf("session not found: %s", payload.SessionID)
		return
	}

	switch payload.Action {
	case "send":
		h.store.SaveMessage(store.MessageRow{
			SessionID: payload.SessionID,
			Role:      "user",
			Type:      "markdown",
			Content:   payload.Message,
		})

		if err := sess.WriteInput(payload.Message); err != nil {
			log.Printf("error writing to session: %v", err)
			errEnv, _ := NewEnvelope("err", "chat", ChatPayload{
				Action:    "error",
				SessionID: payload.SessionID,
				Message:   err.Error(),
			})
			data, _ := errEnv.Marshal()
			client.Send <- data
		}
	}
}

func (h *Handler) handleControlMessage(client *WSClient, env *MessageEnvelope) {
	var payload ControlPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}

	sess, err := h.sm.Get(payload.SessionID)
	if err != nil {
		return
	}

	switch payload.Action {
	case "interrupt":
		sess.Interrupt()
	}
}

func (h *Handler) handleSystemMessage(client *WSClient, env *MessageEnvelope) {
	var payload SystemPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}

	switch payload.Action {
	case "ping":
		hub := h.GetOrCreateHub(client.SessionID)
		hub.BroadcastJSON(map[string]interface{}{
			"id":   "pong-" + payload.Message,
			"type": "system",
			"payload": map[string]string{
				"action":  "pong",
				"message": "pong",
			},
		})
	}
}
