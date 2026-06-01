package transport

import (
	"net/http"

	"phone-claude-bridge/session"
	"phone-claude-bridge/store"
)

// Handler holds HTTP handler dependencies.
type Handler struct {
	sm *session.SessionManager
	db *store.Store
}

// NewHandler creates a new Handler.
func NewHandler(sm *session.SessionManager, db *store.Store) *Handler {
	return &Handler{sm: sm, db: db}
}

// RegisterRoutes registers HTTP routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// TODO: register routes
}
