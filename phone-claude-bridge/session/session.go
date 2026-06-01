package session

import (
	"fmt"
	"sync"
	"time"
)

type SessionStatus string

const (
	StatusRunning SessionStatus = "running"
	StatusIdle    SessionStatus = "idle"
	StatusError   SessionStatus = "error"
	StatusClosed  SessionStatus = "closed"
)

type Session struct {
	ID         string
	Name       string
	WorkDir    string
	Status     SessionStatus
	CreatedAt  time.Time
	LastActive time.Time

	pty    *ConPTYWrapper

	mu      sync.RWMutex
	closing bool
}

func NewSession(id, name, workDir string) *Session {
	now := time.Now()
	return &Session{
		ID:         id,
		Name:       name,
		WorkDir:    workDir,
		Status:     StatusRunning,
		CreatedAt:  now,
		LastActive: now,
	}
}

func (s *Session) TouchActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActive = time.Now()
}

func (s *Session) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

func (s *Session) GetStatus() SessionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status
}

func (s *Session) IsClosing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closing
}

func (s *Session) SetClosing(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = v
}

func (s *Session) Start(binary, workDir string, env map[string]string) error {
	pty, err := StartClaude(workDir, binary, env)
	if err != nil {
		return fmt.Errorf("start claude: %w", err)
	}
	s.pty = pty
	return nil
}

func (s *Session) Stop() error {
	s.SetClosing(true)
	if s.pty != nil {
		s.pty.Write([]byte{0x03})
		time.Sleep(500 * time.Millisecond)
		return s.pty.Close()
	}
	return nil
}

func (s *Session) Interrupt() error {
	if s.pty != nil {
		_, err := s.pty.Write([]byte{0x03})
		return err
	}
	return fmt.Errorf("no running process")
}

func (s *Session) WriteInput(text string) error {
	if s.pty == nil {
		return fmt.Errorf("no running process")
	}
	_, err := s.pty.WriteString(text + "\n")
	s.TouchActivity()
	return err
}

func (s *Session) ReadOutput(buf []byte) (int, error) {
	if s.pty == nil {
		return 0, fmt.Errorf("no running process")
	}
	return s.pty.Read(buf)
}

func (s *Session) ResizeTerminal(cols, rows uint16) error {
	if s.pty == nil {
		return nil
	}
	return s.pty.Resize(cols, rows)
}
