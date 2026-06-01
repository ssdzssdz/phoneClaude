package session

import (
	"phone-claude-bridge/config"
	"phone-claude-bridge/store"
)

// SessionManager manages Claude Code sessions.
type SessionManager struct {
	cfg *config.Config
	db  *store.Store
}

// NewManager creates a new SessionManager.
func NewManager(cfg *config.Config, db *store.Store) *SessionManager {
	return &SessionManager{cfg: cfg, db: db}
}

// StartIdleReaper starts a background goroutine that reaps idle sessions.
func (sm *SessionManager) StartIdleReaper() {
	// TODO: implement idle reaper
}
