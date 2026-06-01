package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"phone-claude-bridge/config"
	"phone-claude-bridge/store"
)

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	cfg      *config.Config
	store    *store.Store
}

func NewManager(cfg *config.Config, st *store.Store) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		cfg:      cfg,
		store:    st,
	}
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "sess_" + hex.EncodeToString(b)
}

func (sm *SessionManager) Create(name, workDir string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.sessions) >= sm.cfg.Session.MaxSessions {
		return nil, fmt.Errorf("max sessions reached (%d)", sm.cfg.Session.MaxSessions)
	}

	if workDir == "" {
		workDir = sm.cfg.Session.WorkDirDefault
	}

	id := generateID()
	sess := NewSession(id, name, workDir)

	if err := sess.Start(sm.cfg.Claude.Binary, workDir, sm.cfg.Claude.Env); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	sm.sessions[id] = sess

	if err := sm.store.CreateSession(store.SessionRow{
		ID:      id,
		Name:    name,
		WorkDir: workDir,
		Status:  string(StatusRunning),
	}); err != nil {
		log.Printf("warn: failed to persist session: %v", err)
	}

	return sess, nil
}

func (sm *SessionManager) Get(id string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sess, ok := sm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return sess, nil
}

func (sm *SessionManager) Destroy(id string) error {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session not found: %s", id)
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	sess.SetStatus(StatusClosed)
	if err := sess.Stop(); err != nil {
		log.Printf("warn: error stopping session %s: %v", id, err)
	}

	if err := sm.store.UpdateSessionStatus(id, string(StatusClosed)); err != nil {
		log.Printf("warn: failed to update session status: %v", err)
	}

	return nil
}

func (sm *SessionManager) List() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		result = append(result, s)
	}
	return result
}

func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

func (sm *SessionManager) StartIdleReaper() {
	idleTimeout, err := time.ParseDuration(sm.cfg.Session.IdleTimeout)
	if err != nil {
		idleTimeout = 30 * time.Minute
	}

	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			sm.mu.RLock()
			now := time.Now()
			var toDestroy []string
			for id, sess := range sm.sessions {
				sess.mu.RLock()
				idle := now.Sub(sess.LastActive) > idleTimeout
				sess.mu.RUnlock()
				if idle {
					toDestroy = append(toDestroy, id)
				}
			}
			sm.mu.RUnlock()

			for _, id := range toDestroy {
				log.Printf("reaping idle session: %s", id)
				sm.Destroy(id)
			}
		}
	}()
}
