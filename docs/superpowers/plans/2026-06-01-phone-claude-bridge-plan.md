# Phone Claude Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go-based Windows bridge service that wraps Claude Code sessions and a Flutter mobile app that connects to it for remote conversation.

**Architecture:** Go daemon runs on Windows, manages Claude Code subprocesses via ConPTY, exposes WebSocket + HTTP API on Tailscale IP. Flutter app connects via WebSocket for real-time chat and HTTP REST for management. PIN-based pairing + JWT auth.

**Tech Stack:** Go 1.22+ (gorilla/websocket, conpty, go-sqlite3, golang-jwt), Flutter 3.22+ (web_socket_channel, flutter_riverpod, flutter_markdown), Tailscale (networking), Firebase Cloud Messaging (push)

---

## File Structure

### Go Bridge Service (`phone-claude-bridge/`)

```
phone-claude-bridge/
├── main.go                     # Entry point, wire up all components, start server
├── go.mod / go.sum
├── config/
│   └── config.go               # YAML config loading, env var overrides
├── auth/
│   ├── handler.go              # POST /auth/pair, /auth/verify, /auth/refresh
│   └── middleware.go            # JWT validation middleware for HTTP + WS
├── session/
│   ├── manager.go               # SessionManager: create/destroy/list sessions, idle reaper
│   ├── session.go               # Session struct, Start/Stop/Interrupt methods
│   └── process.go               # ConPTY wrapper: spawn, read/write, graceful kill
├── transport/
│   ├── ws.go                    # WebSocket upgrade, hub per session, message routing
│   ├── handler.go               # HTTP REST handlers for sessions, files, health
│   └── message.go               # Message envelope types, JSON marshal/unmarshal
├── filebrowser/
│   └── browser.go               # Directory listing for @ autocomplete
├── notify/
│   └── fcm.go                   # Firebase Cloud Messaging push wrapper
├── store/
│   └── sqlite.go                # SQLite: message history, device tokens, sessions
└── phone-claude-bridge.yaml     # Default config file
```

### Flutter App (`phone_claude_app/`)

```
phone_claude_app/
├── pubspec.yaml
├── lib/
│   ├── main.dart                # App entry, ProviderScope, MaterialApp.router
│   ├── app_router.dart          # GoRouter route definitions
│   ├── models/
│   │   ├── message.dart         # MessageEnvelope, MessageBlock, BlockType
│   │   ├── session.dart         # SessionInfo, SessionStatus
│   │   └── connection.dart      # ConnectionState, ConnectionStatus
│   ├── providers/
│   │   ├── connection_provider.dart  # ConnectionStateNotifier
│   │   ├── session_provider.dart     # SessionListNotifier
│   │   └── chat_provider.dart        # ChatNotifier (family by sessionId)
│   ├── services/
│   │   ├── api_service.dart          # HTTP client for REST endpoints
│   │   └── ws_service.dart           # WebSocket manager singleton
│   ├── pages/
│   │   ├── splash_page.dart
│   │   ├── pairing_page.dart
│   │   ├── session_list_page.dart
│   │   ├── chat_page.dart
│   │   └── settings_page.dart
│   └── widgets/
│       ├── message_bubble.dart
│       ├── code_block.dart
│       ├── thinking_indicator.dart
│       └── file_browser_sheet.dart
└── test/
    └── ... (per-page tests)
```

---

## Phase 1: Go Daemon Skeleton

### Task 1.1: Initialize Go module and project structure

**Files:**
- Create: `phone-claude-bridge/go.mod`
- Create: `phone-claude-bridge/main.go`

- [ ] **Step 1: Initialize Go module**

Run: `cd phone-claude-bridge && go mod init phone-claude-bridge`

- [ ] **Step 2: Write minimal main.go**

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("phone-claude-bridge starting...")
	// TODO: load config, init store, start server
	select {} // block forever for now
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd phone-claude-bridge && go build -o phone-claude-bridge.exe .`
Expected: No errors, produces .exe

- [ ] **Step 4: Commit**

```bash
git add phone-claude-bridge/
git commit -m "feat: initialize Go module and main entry point"
```

### Task 1.2: Config loading

**Files:**
- Create: `phone-claude-bridge/config/config.go`
- Create: `phone-claude-bridge/phone-claude-bridge.yaml`

- [ ] **Step 1: Define config struct**

```go
// config/config.go
package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Auth    AuthConfig    `yaml:"auth"`
	Session SessionConfig `yaml:"session"`
	Claude  ClaudeConfig  `yaml:"claude"`
	Notify  NotifyConfig  `yaml:"notify"`
}

type ServerConfig struct {
	ListenTailscale string `yaml:"listen_tailscale"` // "100.x.x.x:9527"
	ListenLocal     string `yaml:"listen_local"`     // "127.0.0.1:9527"
}

type AuthConfig struct {
	JWTSecret string `yaml:"jwt_secret"`
	JWTTTL    string `yaml:"jwt_ttl"`    // "720h"
	PinTTL    string `yaml:"pin_ttl"`    // "5m"
}

type SessionConfig struct {
	MaxSessions    int    `yaml:"max_sessions"`
	IdleTimeout    string `yaml:"idle_timeout"`
	WorkDirDefault string `yaml:"work_dir_default"`
}

type ClaudeConfig struct {
	Binary string            `yaml:"binary"`
	Env    map[string]string `yaml:"env"`
}

type NotifyConfig struct {
	FCMEnabled bool `yaml:"fcm_enabled"`
}

var (
	instance *Config
	once     sync.Once
)

func Load(path string) (*Config, error) {
	var loadErr error
	once.Do(func() {
		instance = &Config{
			Server: ServerConfig{
				ListenTailscale: "100.64.0.1:9527",
				ListenLocal:     "127.0.0.1:9527",
			},
			Auth: AuthConfig{
				JWTSecret: "",
				JWTTTL:    "720h",
				PinTTL:    "5m",
			},
			Session: SessionConfig{
				MaxSessions:    10,
				IdleTimeout:    "30m",
				WorkDirDefault: ".",
			},
			Claude: ClaudeConfig{
				Binary: "claude",
			},
			Notify: NotifyConfig{
				FCMEnabled: true,
			},
		}

		if path == "" {
			path = "phone-claude-bridge.yaml"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				loadErr = nil // use defaults
				return
			}
			loadErr = err
			return
		}
		loadErr = yaml.Unmarshal(data, instance)
	})
	return instance, loadErr
}

func Get() *Config {
	if instance == nil {
		panic("config not loaded, call Load() first")
	}
	return instance
}
```

- [ ] **Step 2: Write default config YAML**

```yaml
# phone-claude-bridge.yaml
server:
  listen_tailscale: "100.64.0.1:9527"
  listen_local: "127.0.0.1:9527"

auth:
  jwt_secret: ""
  jwt_ttl: "720h"
  pin_ttl: "5m"

session:
  max_sessions: 10
  idle_timeout: "30m"
  work_dir_default: "."

claude:
  binary: "claude"
  env:
    ANTHROPIC_BASE_URL: ""
    ANTHROPIC_AUTH_TOKEN: ""
    ANTHROPIC_MODEL: ""

notify:
  fcm_enabled: true
```

- [ ] **Step 3: Install yaml dependency and verify compilation**

Run: `cd phone-claude-bridge && go get gopkg.in/yaml.v3 && go build .`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add phone-claude-bridge/config/ phone-claude-bridge/phone-claude-bridge.yaml
git commit -m "feat: add config loading with YAML + defaults"
```

### Task 1.3: SQLite store

**Files:**
- Create: `phone-claude-bridge/store/sqlite.go`

- [ ] **Step 1: Write store with migrations, message and session persistence**

```go
// store/sqlite.go
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type MessageRow struct {
	ID        int64
	SessionID string
	Role      string // "user" | "assistant" | "system"
	Type      string // chat delta type
	Content   string
	CreatedAt time.Time
}

type SessionRow struct {
	ID         string
	Name       string
	WorkDir    string
	Status     string
	CreatedAt  time.Time
	LastActive time.Time
}

type DeviceRow struct {
	ID        int64
	Token     string // FCM or APNs token
	Name      string
	CreatedAt time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	ddl := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		work_dir TEXT NOT NULL DEFAULT '.',
		status TEXT NOT NULL DEFAULT 'running',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_active DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'markdown',
		content TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

	CREATE TABLE IF NOT EXISTS devices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := s.db.Exec(ddl)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Message methods
func (s *Store) SaveMessage(msg MessageRow) error {
	_, err := s.db.Exec(
		"INSERT INTO messages (session_id, role, type, content) VALUES (?, ?, ?, ?)",
		msg.SessionID, msg.Role, msg.Type, msg.Content,
	)
	return err
}

func (s *Store) GetMessages(sessionID string, limit int) ([]MessageRow, error) {
	rows, err := s.db.Query(
		"SELECT id, session_id, role, type, content, created_at FROM messages WHERE session_id = ? ORDER BY created_at DESC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Type, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// Session methods
func (s *Store) CreateSession(sess SessionRow) error {
	_, err := s.db.Exec(
		"INSERT INTO sessions (id, name, work_dir, status) VALUES (?, ?, ?, ?)",
		sess.ID, sess.Name, sess.WorkDir, sess.Status,
	)
	return err
}

func (s *Store) UpdateSessionStatus(id, status string) error {
	_, err := s.db.Exec(
		"UPDATE sessions SET status = ?, last_active = CURRENT_TIMESTAMP WHERE id = ?",
		status, id,
	)
	return err
}

func (s *Store) TouchSession(id string) error {
	_, err := s.db.Exec("UPDATE sessions SET last_active = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (s *Store) ListSessions() ([]SessionRow, error) {
	rows, err := s.db.Query(
		"SELECT id, name, work_dir, status, created_at, last_active FROM sessions ORDER BY last_active DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []SessionRow
	for rows.Next() {
		var s SessionRow
		if err := rows.Scan(&s.ID, &s.Name, &s.WorkDir, &s.Status, &s.CreatedAt, &s.LastActive); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// Device methods
func (s *Store) SaveDevice(device DeviceRow) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO devices (token, name) VALUES (?, ?)",
		device.Token, device.Name,
	)
	return err
}

func (s *Store) ListDevices() ([]DeviceRow, error) {
	rows, err := s.db.Query("SELECT id, token, name, created_at FROM devices ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []DeviceRow
	for rows.Next() {
		var d DeviceRow
		if err := rows.Scan(&d.ID, &d.Token, &d.Name, &d.CreatedAt); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}
```

- [ ] **Step 2: Install dependency and verify compilation**

Run: `cd phone-claude-bridge && go get github.com/mattn/go-sqlite3 && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/store/
git commit -m "feat: add SQLite store with messages, sessions, devices"
```

### Task 1.4: Wire up main.go with config and store

**Files:**
- Modify: `phone-claude-bridge/main.go`

- [ ] **Step 1: Update main.go to load config and init store**

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"phone-claude-bridge/config"
	"phone-claude-bridge/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Init store
	db, err := store.Open("phone-claude-bridge.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	log.Printf("phone-claude-bridge starting on %s (local: %s)",
		cfg.Server.ListenTailscale, cfg.Server.ListenLocal)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/main.go phone-claude-bridge/go.mod phone-claude-bridge/go.sum
git commit -m "feat: wire up main with config loading and SQLite store"
```

---

## Phase 2: ConPTY Integration + Session Management

### Task 2.1: Message envelope types

**Files:**
- Create: `phone-claude-bridge/transport/message.go`

- [ ] **Step 1: Write message types**

```go
// transport/message.go
package transport

import (
	"encoding/json"
	"time"
)

type MessageEnvelope struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`    // chat | control | file | session | system
	TS      int64           `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

type ChatPayload struct {
	Action      string `json:"action"`       // send | delta | done | error
	SessionID   string `json:"session_id"`
	Message     string `json:"message,omitempty"`
	Content     string `json:"content,omitempty"`
	DeltaIndex  int    `json:"delta_index,omitempty"`
	TotalTokens int    `json:"total_tokens,omitempty"`
	UsageCost   float64 `json:"usage_cost,omitempty"`
}

type ControlPayload struct {
	Action    string `json:"action"`     // interrupt | status
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
}

type FilePayload struct {
	Action    string   `json:"action"`   // autocomplete | autocomplete_result | list | list_result
	SessionID string   `json:"session_id,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Path      string   `json:"path,omitempty"`
	Matches   []string `json:"matches,omitempty"`
	Entries   []FileEntry `json:"entries,omitempty"`
}

type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

type SystemPayload struct {
	Action    string `json:"action"`     // ping | pong | notify
	Level     string `json:"level,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type SessionPayload struct {
	Action    string `json:"action"`     // switch | info | list
	SessionID string `json:"session_id,omitempty"`
	Name      string `json:"name,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
}

func NewEnvelope(id, msgType string, payload interface{}) (*MessageEnvelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &MessageEnvelope{
		ID:      id,
		Type:    msgType,
		TS:      time.Now().Unix(),
		Payload: data,
	}, nil
}

func (m *MessageEnvelope) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func UnmarshalEnvelope(data []byte) (*MessageEnvelope, error) {
	var m MessageEnvelope
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/transport/message.go
git commit -m "feat: add WebSocket message envelope types"
```

### Task 2.2: Session struct and ConPTY process management

**Files:**
- Create: `phone-claude-bridge/session/session.go`
- Create: `phone-claude-bridge/session/process.go`

- [ ] **Step 1: Write session.go**

```go
// session/session.go
package session

import (
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

	// ConPTY process
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
```

- [ ] **Step 2: Write process.go with ConPTY wrapper**

```go
// session/process.go
package session

import (
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/UserExistsError/conpty"
)

type ConPTYWrapper struct {
	cpty  *conpty.ConPty
	stdin io.WriteCloser
	cmd   *exec.Cmd
	mu    sync.Mutex
}

func StartClaude(workDir, binary string, env map[string]string) (*ConPTYWrapper, error) {
	cmd := exec.Command(binary)
	cmd.Dir = workDir

	// Set environment variables
	if env != nil {
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	cpty, err := conpty.Start(cmd, conpty.ConPtyDimensions(120, 40))
	if err != nil {
		return nil, fmt.Errorf("conpty start: %w", err)
	}

	return &ConPTYWrapper{
		cpty:  cpty,
		stdin: cpty.In(),
		cmd:   cmd,
	}, nil
}

func (w *ConPTYWrapper) Read(p []byte) (int, error) {
	return w.cpty.Out().Read(p)
}

func (w *ConPTYWrapper) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stdin.Write(data)
}

func (w *ConPTYWrapper) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *ConPTYWrapper) Resize(cols, rows uint16) error {
	return w.cpty.Resize(cols, rows)
}

func (w *ConPTYWrapper) Close() error {
	if err := w.stdin.Close(); err != nil {
		// Ignore close errors
	}
	return w.cpty.Close()
}

func (w *ConPTYWrapper) IsRunning() bool {
	return w.cpty != nil
}
```

- [ ] **Step 3: Add Start, Stop, Interrupt methods to Session**

Add to `session/session.go`:

```go
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
		// Send Ctrl+C to interrupt any running operation
		s.pty.Write([]byte{0x03})
		// Short pause then close
		time.Sleep(500 * time.Millisecond)
		return s.pty.Close()
	}
	return nil
}

func (s *Session) Interrupt() error {
	if s.pty != nil {
		_, err := s.pty.Write([]byte{0x03}) // Ctrl+C
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
```

Make sure to add imports `"fmt"` and `"time"` to session.go.

- [ ] **Step 4: Install conpty dependency and verify compilation**

Run: `cd phone-claude-bridge && go get github.com/UserExistsError/conpty && go build .`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add phone-claude-bridge/session/
git commit -m "feat: add Session struct and ConPTY process management"
```

### Task 2.3: SessionManager with idle reaper

**Files:**
- Create: `phone-claude-bridge/session/manager.go`

- [ ] **Step 1: Write SessionManager**

```go
// session/manager.go
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

	// Persist to SQLite
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
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/session/manager.go
git commit -m "feat: add SessionManager with idle reaper"
```

---

## Phase 3: WebSocket Real-time Communication

### Task 3.1: WebSocket hub per session

**Files:**
- Create: `phone-claude-bridge/transport/ws.go`

- [ ] **Step 1: Write WebSocket hub**

```go
// transport/ws.go
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
```

- [ ] **Step 2: Install gorilla/websocket and verify compilation**

Run: `cd phone-claude-bridge && go get github.com/gorilla/websocket && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/transport/ws.go
git commit -m "feat: add WebSocket hub with client management"
```

### Task 3.2: HTTP handlers for REST API

**Files:**
- Create: `phone-claude-bridge/transport/handler.go`

- [ ] **Step 1: Write HTTP handlers**

```go
// transport/handler.go
package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"phone-claude-bridge/session"
	"phone-claude-bridge/store"
)

type Handler struct {
	sm       *session.SessionManager
	store    *store.Store
	sessions map[string]*WSHub // session_id -> hub
}

func NewHandler(sm *session.SessionManager, st *store.Store) *Handler {
	return &Handler{
		sm:       sm,
		store:    st,
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
	mux.HandleFunc("/api/v1/sessions", h.handleSessions)
	mux.HandleFunc("/api/v1/sessions/", h.handleSessionByID)
	mux.HandleFunc("/api/v1/fs/", h.handleFS)
	mux.HandleFunc("/ws", h.handleWebSocket)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
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
	// Extract session ID from URL: /api/v1/sessions/{id}
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
	// Delegate to filebrowser package
	// Implemented in Phase 7
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

	// Write pump: send queued messages to the client
	go func() {
		for msg := range client.Send {
			if err := client.WriteMessage(msg); err != nil {
				log.Printf("ws write error: %v", err)
				return
			}
		}
	}()

	// Read pump: receive messages from the client and handle them
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
		// Save user message to store
		h.store.SaveMessage(store.MessageRow{
			SessionID: payload.SessionID,
			Role:      "user",
			Type:      "markdown",
			Content:   payload.Message,
		})

		// Write to Claude Code stdin
		if err := sess.WriteInput(payload.Message); err != nil {
			log.Printf("error writing to session: %v", err)
			// Send error back
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
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/transport/handler.go
git commit -m "feat: add HTTP REST handlers and WebSocket read/write pump"
```

### Task 3.3: Wire up HTTP server in main.go

**Files:**
- Modify: `phone-claude-bridge/main.go`

- [ ] **Step 1: Update main.go with HTTP server startup**

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"phone-claude-bridge/config"
	"phone-claude-bridge/session"
	"phone-claude-bridge/store"
	"phone-claude-bridge/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.Open("phone-claude-bridge.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	sm := session.NewManager(cfg, db)
	sm.StartIdleReaper()

	handler := transport.NewHandler(sm, db)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Start local listener
	localServer := &http.Server{
		Addr:    cfg.Server.ListenLocal,
		Handler: mux,
	}
	go func() {
		log.Printf("listening on %s (local)", cfg.Server.ListenLocal)
		if err := localServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("local server error: %v", err)
		}
	}()

	// Start Tailscale listener
	tsServer := &http.Server{
		Addr:    cfg.Server.ListenTailscale,
		Handler: mux,
	}
	go func() {
		log.Printf("listening on %s (tailscale)", cfg.Server.ListenTailscale)
		if err := tsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("tailscale server error: %v", err)
		}
	}()

	log.Println("phone-claude-bridge is running")

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/main.go
git commit -m "feat: wire up HTTP server with dual listener (local + tailscale)"
```

---

## Phase 4: Auth System

### Task 4.1: JWT auth with PIN pairing

**Files:**
- Create: `phone-claude-bridge/auth/handler.go`
- Create: `phone-claude-bridge/auth/middleware.go`

- [ ] **Step 1: Write auth handler with PIN generation and JWT**

```go
// auth/handler.go
package auth

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"phone-claude-bridge/config"
)

type PairingSession struct {
	PIN       string
	DeviceName string
	ExpiresAt time.Time
}

type AuthHandler struct {
	mu             sync.Mutex
	pairingSessions map[string]*PairingSession // pin -> session
	cfg            *config.Config
}

func NewAuthHandler(cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		pairingSessions: make(map[string]*PairingSession),
		cfg:            cfg,
	}
}

func (h *AuthHandler) generatePIN() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("%04d", n.Int64()+1000)
}

func (h *AuthHandler) getSecret() []byte {
	secret := h.cfg.Auth.JWTSecret
	if secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		secret = fmt.Sprintf("%x", b)
	}
	return []byte(secret)
}

func (h *AuthHandler) getJWTExpiry() time.Duration {
	d, err := time.ParseDuration(h.cfg.Auth.JWTTTL)
	if err != nil {
		d = 720 * time.Hour // 30 days
	}
	return d
}

func (h *AuthHandler) HandlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceName string `json:"device_name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	pin := h.generatePIN()
	h.mu.Lock()
	h.pairingSessions[pin] = &PairingSession{
		PIN:        pin,
		DeviceName: req.DeviceName,
		ExpiresAt:  time.Now().Add(5 * time.Minute),
	}
	h.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{
		"pin":         pin,
		"expires_in":  "300",
		"device_name": req.DeviceName,
	})
}

func (h *AuthHandler) HandleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PIN        string `json:"pin"`
		DeviceName string `json:"device_name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	h.mu.Lock()
	session, ok := h.pairingSessions[req.PIN]
	if ok {
		delete(h.pairingSessions, req.PIN)
	}
	h.mu.Unlock()

	if !ok || time.Now().After(session.ExpiresAt) {
		http.Error(w, `{"error":"invalid or expired PIN"}`, http.StatusUnauthorized)
		return
	}

	// Generate JWT
	claims := jwt.MapClaims{
		"device_name": req.DeviceName,
		"iat":         time.Now().Unix(),
		"exp":         time.Now().Add(h.getJWTExpiry()).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(h.getSecret())
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"token":      tokenString,
		"expires_in": fmt.Sprintf("%.0f", h.getJWTExpiry().Seconds()),
	})
}

func (h *AuthHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	// Token refresh — validate existing JWT and issue a new one
	tokenString := r.Header.Get("Authorization")
	if len(tokenString) < 8 || tokenString[:7] != "Bearer " {
		http.Error(w, `{"error":"missing or invalid authorization header"}`, http.StatusUnauthorized)
		return
	}
	tokenString = tokenString[7:]

	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return h.getSecret(), nil
	})
	if err != nil || !token.Valid {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, `{"error":"invalid claims"}`, http.StatusUnauthorized)
		return
	}

	newClaims := jwt.MapClaims{
		"device_name": claims["device_name"],
		"iat":         time.Now().Unix(),
		"exp":         time.Now().Add(h.getJWTExpiry()).Unix(),
	}
	newToken := jwt.NewWithClaims(jwt.SigningMethodHS256, newClaims)
	newTokenString, _ := newToken.SignedString(h.getSecret())

	json.NewEncoder(w).Encode(map[string]string{
		"token":      newTokenString,
		"expires_in": fmt.Sprintf("%.0f", h.getJWTExpiry().Seconds()),
	})
}

func (h *AuthHandler) CleanupExpiredPINs() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			h.mu.Lock()
			now := time.Now()
			for pin, session := range h.pairingSessions {
				if now.After(session.ExpiresAt) {
					delete(h.pairingSessions, pin)
				}
			}
			h.mu.Unlock()
		}
	}()
}
```

- [ ] **Step 2: Write JWT middleware**

```go
// auth/middleware.go
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const ClaimsKey contextKey = "claims"

func (h *AuthHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for pairing endpoints and health
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/auth/") ||
			path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token from Authorization header or query param (for WS)
		tokenString := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = authHeader[7:]
		} else {
			tokenString = r.URL.Query().Get("token")
		}

		if tokenString == "" {
			http.Error(w, `{"error":"missing authentication"}`, http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			return h.getSecret(), nil
		})
		if err != nil || !token.Valid {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, `{"error":"invalid token claims"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

- [ ] **Step 3: Install jwt dependency and verify compilation**

Run: `cd phone-claude-bridge && go get github.com/golang-jwt/jwt/v5 && go build .`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add phone-claude-bridge/auth/
git commit -m "feat: add PIN pairing and JWT authentication"
```

### Task 4.2: Add auth routes and middleware to main

**Files:**
- Modify: `phone-claude-bridge/transport/handler.go` — add auth routes
- Modify: `phone-claude-bridge/main.go` — apply middleware

- [ ] **Step 1: Update handler.go to accept AuthHandler and register auth routes**

Add to `Handler` struct:
```go
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
```

Add auth routes to `RegisterRoutes`:
```go
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
```

Update the import to include `"phone-claude-bridge/auth"`.

- [ ] **Step 2: Update main.go to create AuthHandler and apply middleware**

```go
func run() error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.Open("phone-claude-bridge.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	sm := session.NewManager(cfg, db)
	sm.StartIdleReaper()

	ah := auth.NewAuthHandler(cfg)
	ah.CleanupExpiredPINs()

	handler := transport.NewHandler(sm, db, ah)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Wrap with auth middleware
	authMux := ah.Middleware(mux)

	// Start servers with authMux instead of mux
	localServer := &http.Server{
		Addr:    cfg.Server.ListenLocal,
		Handler: authMux,
	}
	// ... rest same as before, replace mux with authMux
```

- [ ] **Step 3: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add phone-claude-bridge/transport/handler.go phone-claude-bridge/main.go
git commit -m "feat: add auth routes and JWT middleware to server"
```

---

## Phase 5: Flutter App Basics

### Task 5.1: Create Flutter project and dependencies

**Files:**
- Create: `phone_claude_app/pubspec.yaml`
- Create: `phone_claude_app/lib/main.dart`
- Create: `phone_claude_app/lib/models/`

- [ ] **Step 1: Create Flutter project**

Run: `cd /d/codeAgent/phoneClaude && flutter create phone_claude_app --org com.phoneclaude --platforms ios,android`

- [ ] **Step 2: Update pubspec.yaml with dependencies**

```yaml
name: phone_claude_app
description: Mobile client for Phone Claude Bridge
publish_to: 'none'
version: 1.0.0+1

environment:
  sdk: '>=3.4.0 <4.0.0'

dependencies:
  flutter:
    sdk: flutter
  flutter_riverpod: ^2.5.1
  go_router: ^14.2.0
  web_socket_channel: ^2.4.5
  http: ^1.2.1
  shared_preferences: ^2.2.3
  flutter_markdown: ^0.7.1
  flutter_highlight: ^0.7.0
  highlight: ^0.7.0
  firebase_core: ^2.31.0
  firebase_messaging: ^14.9.1
  uuid: ^4.4.0
  json_annotation: ^4.9.0

dev_dependencies:
  flutter_test:
    sdk: flutter
  flutter_lints: ^4.0.0
  json_serializable: ^6.8.0
  build_runner: ^2.4.9

flutter:
  uses-material-design: true
```

Run: `cd phone_claude_app && flutter pub get`

- [ ] **Step 3: Write model classes**

```dart
// lib/models/connection.dart
enum ConnectionStatus { disconnected, connecting, connected, paired }

class ConnectionState {
  final ConnectionStatus status;
  final String? host;
  final String? token;
  final String? error;

  const ConnectionState({
    this.status = ConnectionStatus.disconnected,
    this.host,
    this.token,
    this.error,
  });

  ConnectionState copyWith({
    ConnectionStatus? status,
    String? host,
    String? token,
    String? error,
  }) {
    return ConnectionState(
      status: status ?? this.status,
      host: host ?? this.host,
      token: token ?? this.token,
      error: error,
    );
  }
}
```

```dart
// lib/models/session.dart
class SessionInfo {
  final String id;
  final String name;
  final String workDir;
  final String status;
  final int lastActive;

  const SessionInfo({
    required this.id,
    required this.name,
    required this.workDir,
    required this.status,
    required this.lastActive,
  });

  factory SessionInfo.fromJson(Map<String, dynamic> json) {
    return SessionInfo(
      id: json['id'] as String,
      name: json['name'] as String,
      workDir: json['work_dir'] as String? ?? '',
      status: json['status'] as String? ?? 'running',
      lastActive: json['last_active'] as int? ?? 0,
    );
  }

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'work_dir': workDir,
    'status': status,
    'last_active': lastActive,
  };
}
```

```dart
// lib/models/message.dart
enum BlockType { markdown, code, thinking, toolCall, toolResult, error }

class MessageBlock {
  final BlockType type;
  final String content;
  final Map<String, dynamic>? metadata;

  const MessageBlock({
    required this.type,
    required this.content,
    this.metadata,
  });

  factory MessageBlock.fromJson(Map<String, dynamic> json) {
    return MessageBlock(
      type: BlockType.values.firstWhere(
        (e) => e.name == json['type'],
        orElse: () => BlockType.markdown,
      ),
      content: json['content'] as String? ?? '',
      metadata: json['metadata'] as Map<String, dynamic>?,
    );
  }

  Map<String, dynamic> toJson() => {
    'type': type.name,
    'content': content,
    if (metadata != null) 'metadata': metadata,
  };
}

class ChatMessage {
  final String id;
  final String sessionId;
  final String role; // user | assistant | system
  final List<MessageBlock> blocks;
  final int timestamp;
  final bool isStreaming;

  const ChatMessage({
    required this.id,
    required this.sessionId,
    required this.role,
    this.blocks = const [],
    required this.timestamp,
    this.isStreaming = false,
  });

  ChatMessage copyWith({
    List<MessageBlock>? blocks,
    bool? isStreaming,
  }) {
    return ChatMessage(
      id: id,
      sessionId: sessionId,
      role: role,
      blocks: blocks ?? this.blocks,
      timestamp: timestamp,
      isStreaming: isStreaming ?? this.isStreaming,
    );
  }
}

class MessageEnvelope {
  final String id;
  final String type;
  final int ts;
  final Map<String, dynamic> payload;

  const MessageEnvelope({
    required this.id,
    required this.type,
    required this.ts,
    required this.payload,
  });

  factory MessageEnvelope.fromJson(Map<String, dynamic> json) {
    return MessageEnvelope(
      id: json['id'] as String,
      type: json['type'] as String,
      ts: json['ts'] as int,
      payload: json['payload'] as Map<String, dynamic>,
    );
  }

  Map<String, dynamic> toJson() => {
    'id': id,
    'type': type,
    'ts': ts,
    'payload': payload,
  };
}
```

- [ ] **Step 4: Verify project compiles**

Run: `cd phone_claude_app && flutter analyze`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add phone_claude_app/
git commit -m "feat: create Flutter project with models and dependencies"
```

### Task 5.2: API service and WebSocket service

**Files:**
- Create: `phone_claude_app/lib/services/api_service.dart`
- Create: `phone_claude_app/lib/services/ws_service.dart`

- [ ] **Step 1: Write API service**

```dart
// lib/services/api_service.dart
import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/session.dart';

class ApiService {
  final String baseUrl;
  final String token;

  ApiService({required this.baseUrl, required this.token});

  Map<String, String> get _headers => {
    'Authorization': 'Bearer $token',
    'Content-Type': 'application/json',
  };

  // Auth
  Future<Map<String, dynamic>> requestPair(String deviceName) async {
    final resp = await http.post(
      Uri.parse('$baseUrl/api/v1/auth/pair'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'device_name': deviceName}),
    );
    return jsonDecode(resp.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> verifyPin(String pin, String deviceName) async {
    final resp = await http.post(
      Uri.parse('$baseUrl/api/v1/auth/verify'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'pin': pin, 'device_name': deviceName}),
    );
    return jsonDecode(resp.body) as Map<String, dynamic>;
  }

  // Sessions
  Future<List<SessionInfo>> listSessions() async {
    final resp = await http.get(
      Uri.parse('$baseUrl/api/v1/sessions'),
      headers: _headers,
    );
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    final list = data['sessions'] as List<dynamic>;
    return list.map((s) => SessionInfo.fromJson(s as Map<String, dynamic>)).toList();
  }

  Future<SessionInfo> createSession(String name, String workDir) async {
    final resp = await http.post(
      Uri.parse('$baseUrl/api/v1/sessions'),
      headers: _headers,
      body: jsonEncode({'name': name, 'work_dir': workDir}),
    );
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    return SessionInfo(
      id: data['id'] as String,
      name: data['name'] as String,
      workDir: '',
      status: 'running',
      lastActive: 0,
    );
  }

  Future<void> deleteSession(String id) async {
    await http.delete(
      Uri.parse('$baseUrl/api/v1/sessions/$id'),
      headers: _headers,
    );
  }

  // Files
  Future<List<Map<String, dynamic>>> listFiles(String path) async {
    final resp = await http.post(
      Uri.parse('$baseUrl/api/v1/fs/list'),
      headers: _headers,
      body: jsonEncode({'path': path}),
    );
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    final entries = data['entries'] as List<dynamic>? ?? [];
    return entries.cast<Map<String, dynamic>>();
  }

  // Health
  Future<bool> healthCheck() async {
    try {
      final resp = await http.get(Uri.parse('$baseUrl/api/v1/health'));
      return resp.statusCode == 200;
    } catch (_) {
      return false;
    }
  }
}
```

- [ ] **Step 2: Write WebSocket service**

```dart
// lib/services/ws_service.dart
import 'dart:async';
import 'dart:convert';
import 'package:uuid/uuid.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import '../models/message.dart';

class WebSocketService {
  WebSocketChannel? _channel;
  final String baseUrl;
  final String token;
  final _messageController = StreamController<MessageEnvelope>.broadcast();
  final _uuid = const Uuid();

  Stream<MessageEnvelope> get messages => _messageController.stream;

  WebSocketService({required this.baseUrl, required this.token});

  String get wsUrl => baseUrl
      .replaceFirst('http://', 'ws://')
      .replaceFirst('https://', 'wss://');

  Future<void> connect(String sessionId) async {
    disconnect();
    final uri = Uri.parse('$wsUrl/ws?token=$token&session_id=$sessionId');
    _channel = WebSocketChannel.connect(uri);

    _channel!.stream.listen(
      (data) {
        final json = jsonDecode(data as String) as Map<String, dynamic>;
        final envelope = MessageEnvelope.fromJson(json);
        _messageController.add(envelope);
      },
      onDone: () {},
      onError: (error) {},
    );
  }

  void sendMessage(String sessionId, String text) {
    if (_channel == null) return;
    final envelope = {
      'id': _uuid.v4(),
      'type': 'chat',
      'ts': DateTime.now().millisecondsSinceEpoch ~/ 1000,
      'payload': {
        'action': 'send',
        'session_id': sessionId,
        'message': text,
      },
    };
    _channel!.sink.add(jsonEncode(envelope));
  }

  void interruptSession(String sessionId) {
    if (_channel == null) return;
    final envelope = {
      'id': _uuid.v4(),
      'type': 'control',
      'ts': DateTime.now().millisecondsSinceEpoch ~/ 1000,
      'payload': {
        'action': 'interrupt',
        'session_id': sessionId,
      },
    };
    _channel!.sink.add(jsonEncode(envelope));
  }

  void sendPing() {
    if (_channel == null) return;
    final envelope = {
      'id': _uuid.v4(),
      'type': 'system',
      'ts': DateTime.now().millisecondsSinceEpoch ~/ 1000,
      'payload': {'action': 'ping', 'message': 'ping'},
    };
    _channel!.sink.add(jsonEncode(envelope));
  }

  void disconnect() {
    _channel?.sink.close();
    _channel = null;
  }

  void dispose() {
    disconnect();
    _messageController.close();
  }
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd phone_claude_app && flutter analyze`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add phone_claude_app/lib/services/
git commit -m "feat: add API service and WebSocket service"
```

### Task 5.3: Providers (Riverpod state management)

**Files:**
- Create: `phone_claude_app/lib/providers/connection_provider.dart`
- Create: `phone_claude_app/lib/providers/session_provider.dart`
- Create: `phone_claude_app/lib/providers/chat_provider.dart`

- [ ] **Step 1: Write connection provider**

```dart
// lib/providers/connection_provider.dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/connection.dart';
import '../services/api_service.dart';
import '../services/ws_service.dart';

class ConnectionNotifier extends StateNotifier<ConnectionState> {
  ConnectionNotifier() : super(const ConnectionState());

  ApiService? apiService;
  WebSocketService? wsService;

  Future<void> loadSavedConnection() async {
    final prefs = await SharedPreferences.getInstance();
    final host = prefs.getString('bridge_host');
    final token = prefs.getString('bridge_token');
    if (host != null && token != null) {
      state = state.copyWith(
        status: ConnectionStatus.paired,
        host: host,
        token: token,
      );
      apiService = ApiService(baseUrl: 'http://$host', token: token);
      wsService = WebSocketService(baseUrl: 'http://$host', token: token);
    }
  }

  Future<void> saveConnection(String host, String token) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('bridge_host', host);
    await prefs.setString('bridge_token', token);
    state = state.copyWith(
      status: ConnectionStatus.paired,
      host: host,
      token: token,
      error: null,
    );
    apiService = ApiService(baseUrl: 'http://$host', token: token);
    wsService = WebSocketService(baseUrl: 'http://$host', token: token);
  }

  void setConnecting(String host) {
    state = state.copyWith(
      status: ConnectionStatus.connecting,
      host: host,
    );
  }

  void setConnected() {
    state = state.copyWith(status: ConnectionStatus.connected);
  }

  void setError(String error) {
    state = state.copyWith(
      status: ConnectionStatus.disconnected,
      error: error,
    );
  }

  void disconnect() {
    wsService?.disconnect();
    state = const ConnectionState(status: ConnectionStatus.disconnected);
  }
}

final connectionProvider = StateNotifierProvider<ConnectionNotifier, ConnectionState>((ref) {
  return ConnectionNotifier();
});
```

- [ ] **Step 2: Write session provider**

```dart
// lib/providers/session_provider.dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/session.dart';
import 'connection_provider.dart';

class SessionListNotifier extends StateNotifier<AsyncValue<List<SessionInfo>>> {
  final Ref _ref;

  SessionListNotifier(this._ref) : super(const AsyncValue.loading());

  Future<void> loadSessions() async {
    final conn = _ref.read(connectionProvider);
    if (conn.apiService == null) {
      state = AsyncValue.error('Not connected', StackTrace.current);
      return;
    }

    state = const AsyncValue.loading();
    try {
      final sessions = await conn.apiService!.listSessions();
      state = AsyncValue.data(sessions);
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<SessionInfo?> createSession(String name, String workDir) async {
    final conn = _ref.read(connectionProvider);
    if (conn.apiService == null) return null;

    try {
      final session = await conn.apiService!.createSession(name, workDir);
      await loadSessions(); // Refresh list
      return session;
    } catch (e, st) {
      state = AsyncValue.error(e, st);
      return null;
    }
  }

  Future<void> deleteSession(String id) async {
    final conn = _ref.read(connectionProvider);
    if (conn.apiService == null) return;

    try {
      await conn.apiService!.deleteSession(id);
      await loadSessions();
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

final sessionListProvider =
    StateNotifierProvider<SessionListNotifier, AsyncValue<List<SessionInfo>>>((ref) {
  return SessionListNotifier(ref);
});
```

- [ ] **Step 3: Write chat provider**

```dart
// lib/providers/chat_provider.dart
import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/message.dart';
import 'connection_provider.dart';

class ChatNotifier extends StateNotifier<List<ChatMessage>> {
  final String sessionId;
  final Ref _ref;
  StreamSubscription? _subscription;

  ChatNotifier(this.sessionId, this._ref) : super([]);

  void connectAndListen() {
    final conn = _ref.read(connectionProvider);
    if (conn.wsService == null) return;

    conn.wsService!.connect(sessionId);

    _subscription?.cancel();
    _subscription = conn.wsService!.messages.listen((envelope) {
      if (envelope.type == 'chat') {
        final action = envelope.payload['action'] as String?;
        final content = envelope.payload['content'] as String? ?? '';
        final msgSessionId = envelope.payload['session_id'] as String?;

        if (msgSessionId != sessionId) return;

        if (action == 'delta') {
          // Append to last streaming message or create new one
          if (state.isNotEmpty && state.last.isStreaming) {
            final lastMsg = state.last;
            final blocks = List<MessageBlock>.from(lastMsg.blocks);
            if (blocks.isNotEmpty &&
                blocks.last.type == BlockType.markdown) {
              blocks.last = MessageBlock(
                type: blocks.last.type,
                content: blocks.last.content + content,
              );
            } else {
              blocks.add(const MessageBlock(
                type: BlockType.markdown,
                content: content,
              ));
            }
            state = [...state.sublist(0, state.length - 1),
              lastMsg.copyWith(blocks: blocks)];
          } else {
            state = [...state, ChatMessage(
              id: envelope.id,
              sessionId: sessionId,
              role: 'assistant',
              blocks: [MessageBlock(type: BlockType.markdown, content: content)],
              timestamp: envelope.ts,
              isStreaming: true,
            )];
          }
        } else if (action == 'done') {
          if (state.isNotEmpty && state.last.isStreaming) {
            state = [...state.sublist(0, state.length - 1),
              state.last.copyWith(isStreaming: false)];
          }
        } else if (action == 'error') {
          state = [...state, ChatMessage(
            id: envelope.id,
            sessionId: sessionId,
            role: 'system',
            blocks: [MessageBlock(type: BlockType.error, content: content)],
            timestamp: envelope.ts,
          )];
        }
      }
    });
  }

  void sendMessage(String text) {
    final conn = _ref.read(connectionProvider);
    if (conn.wsService == null) return;

    // Add user message immediately
    state = [...state, ChatMessage(
      id: DateTime.now().millisecondsSinceEpoch.toString(),
      sessionId: sessionId,
      role: 'user',
      blocks: [MessageBlock(type: BlockType.markdown, content: text)],
      timestamp: DateTime.now().millisecondsSinceEpoch ~/ 1000,
    )];

    conn.wsService!.sendMessage(sessionId, text);
  }

  void interrupt() {
    final conn = _ref.read(connectionProvider);
    conn.wsService?.interruptSession(sessionId);
  }

  @override
  void dispose() {
    _subscription?.cancel();
    super.dispose();
  }
}

// Family provider: one ChatNotifier per sessionId
final chatProviderFamily = StateNotifierProvider.family<ChatNotifier, List<ChatMessage>, String>(
  (ref, sessionId) {
    return ChatNotifier(sessionId, ref);
  },
);
```

- [ ] **Step 4: Verify compilation**

Run: `cd phone_claude_app && flutter analyze`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add phone_claude_app/lib/providers/
git commit -m "feat: add Riverpod providers for connection, sessions, and chat"
```

---

## Phase 6: Flutter ChatPage

### Task 6.1: Main app entry and routing

**Files:**
- Create: `phone_claude_app/lib/app_router.dart`
- Modify: `phone_claude_app/lib/main.dart`

- [ ] **Step 1: Write app_router.dart**

```dart
// lib/app_router.dart
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'pages/splash_page.dart';
import 'pages/pairing_page.dart';
import 'pages/session_list_page.dart';
import 'pages/chat_page.dart';
import 'pages/settings_page.dart';

final appRouter = GoRouter(
  initialLocation: '/',
  routes: [
    GoRoute(
      path: '/',
      builder: (context, state) => const SplashPage(),
    ),
    GoRoute(
      path: '/pair',
      builder: (context, state) => const PairingPage(),
    ),
    GoRoute(
      path: '/sessions',
      builder: (context, state) => const SessionListPage(),
    ),
    GoRoute(
      path: '/chat/:sessionId',
      builder: (context, state) {
        final sessionId = state.pathParameters['sessionId']!;
        return ChatPage(sessionId: sessionId);
      },
    ),
    GoRoute(
      path: '/settings',
      builder: (context, state) => const SettingsPage(),
    ),
  ],
);
```

- [ ] **Step 2: Write main.dart**

```dart
// lib/main.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'app_router.dart';

void main() {
  runApp(const ProviderScope(child: PhoneClaudeApp()));
}

class PhoneClaudeApp extends StatelessWidget {
  const PhoneClaudeApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp.router(
      title: 'Phone Claude',
      theme: ThemeData(
        colorSchemeSeed: const Color(0xFFD97757),
        brightness: Brightness.dark,
        useMaterial3: true,
      ),
      routerConfig: appRouter,
    );
  }
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd phone_claude_app && flutter analyze`

- [ ] **Step 4: Commit**

```bash
git add phone_claude_app/lib/main.dart phone_claude_app/lib/app_router.dart
git commit -m "feat: add GoRouter and main app entry"
```

### Task 6.2: Placeholder pages and ChatPage UI

**Files:**
- Create: `phone_claude_app/lib/pages/splash_page.dart`
- Create: `phone_claude_app/lib/pages/pairing_page.dart`
- Create: `phone_claude_app/lib/pages/session_list_page.dart`
- Create: `phone_claude_app/lib/pages/chat_page.dart`
- Create: `phone_claude_app/lib/pages/settings_page.dart`

- [ ] **Step 1: Write SplashPage**

```dart
// lib/pages/splash_page.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../providers/connection_provider.dart';

class SplashPage extends ConsumerStatefulWidget {
  const SplashPage({super.key});

  @override
  ConsumerState<SplashPage> createState() => _SplashPageState();
}

class _SplashPageState extends ConsumerState<SplashPage> {
  @override
  void initState() {
    super.initState();
    _tryAutoConnect();
  }

  Future<void> _tryAutoConnect() async {
    await ref.read(connectionProvider.notifier).loadSavedConnection();
    if (!mounted) return;
    final conn = ref.read(connectionProvider);
    if (conn.status == ConnectionStatus.paired) {
      context.go('/sessions');
    } else {
      context.go('/pair');
    }
  }

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      body: Center(child: CircularProgressIndicator()),
    );
  }
}
```

- [ ] **Step 2: Write PairingPage**

```dart
// lib/pages/pairing_page.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../providers/connection_provider.dart';

class PairingPage extends ConsumerStatefulWidget {
  const PairingPage({super.key});

  @override
  ConsumerState<PairingPage> createState() => _PairingPageState();
}

class _PairingPageState extends ConsumerState<PairingPage> {
  final _hostController = TextEditingController();
  final _pinController = TextEditingController();
  final _deviceNameController = TextEditingController(text: 'My Phone');
  String? _currentPin;
  bool _loading = false;

  @override
  void dispose() {
    _hostController.dispose();
    _pinController.dispose();
    _deviceNameController.dispose();
    super.dispose();
  }

  Future<void> _requestPair() async {
    setState(() => _loading = true);
    final conn = ref.read(connectionProvider);
    // Create temporary API service for pairing
    final api = conn.apiService ??
        ApiService(baseUrl: 'http://${_hostController.text.trim()}', token: '');

    try {
      final result = await api.requestPair(_deviceNameController.text.trim());
      setState(() => _currentPin = result['pin'] as String?);
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Connection failed: $e')),
      );
    }
    setState(() => _loading = false);
  }

  Future<void> _verifyPin() async {
    setState(() => _loading = true);
    final api = ApiService(
      baseUrl: 'http://${_hostController.text.trim()}',
      token: '',
    );

    try {
      final result = await api.verifyPin(
        _pinController.text.trim(),
        _deviceNameController.text.trim(),
      );
      final token = result['token'] as String;
      await ref.read(connectionProvider.notifier).saveConnection(
        _hostController.text.trim(),
        token,
      );
      if (mounted) context.go('/sessions');
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Verification failed: $e')),
      );
    }
    setState(() => _loading = false);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Connect to Bridge')),
      body: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            TextField(
              controller: _hostController,
              decoration: const InputDecoration(
                labelText: 'Bridge Address',
                hintText: '100.x.x.x:9527',
                border: OutlineInputBorder(),
              ),
              keyboardType: TextInputType.url,
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _deviceNameController,
              decoration: const InputDecoration(
                labelText: 'Device Name',
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 24),
            if (_currentPin == null)
              ElevatedButton(
                onPressed: _loading ? null : _requestPair,
                child: const Text('Request PIN'),
              )
            else ...[
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.primaryContainer,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Text(
                  'PIN: $_currentPin',
                  style: Theme.of(context).textTheme.headlineLarge,
                ),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _pinController,
                decoration: const InputDecoration(
                  labelText: 'Enter PIN',
                  border: OutlineInputBorder(),
                ),
                keyboardType: TextInputType.number,
                maxLength: 4,
              ),
              const SizedBox(height: 16),
              ElevatedButton(
                onPressed: _loading ? null : _verifyPin,
                child: const Text('Verify & Connect'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
```

- [ ] **Step 3: Write SessionListPage**

```dart
// lib/pages/session_list_page.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../providers/connection_provider.dart';
import '../providers/session_provider.dart';

class SessionListPage extends ConsumerWidget {
  const SessionListPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sessionsAsync = ref.watch(sessionListProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Sessions'),
        actions: [
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: sessionsAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (error, _) => Center(
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Text('Error: $error'),
              const SizedBox(height: 16),
              ElevatedButton(
                onPressed: () => ref.read(sessionListProvider.notifier).loadSessions(),
                child: const Text('Retry'),
              ),
            ],
          ),
        ),
        data: (sessions) {
          if (sessions.isEmpty) {
            return const Center(
              child: Text('No sessions. Tap + to create one.'),
            );
          }
          return ListView.builder(
            itemCount: sessions.length,
            itemBuilder: (context, index) {
              final s = sessions[index];
              return ListTile(
                leading: Icon(
                  s.status == 'running' ? Icons.terminal : Icons.stop_circle,
                  color: s.status == 'running' ? Colors.green : Colors.grey,
                ),
                title: Text(s.name),
                subtitle: Text(s.id),
                trailing: IconButton(
                  icon: const Icon(Icons.delete_outline),
                  onPressed: () {
                    ref.read(sessionListProvider.notifier).deleteSession(s.id);
                  },
                ),
                onTap: () {
                  context.push('/chat/${s.id}');
                },
              );
            },
          );
        },
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: () => _showCreateDialog(context, ref),
        child: const Icon(Icons.add),
      ),
    );
  }

  void _showCreateDialog(BuildContext context, WidgetRef ref) {
    final nameController = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('New Session'),
        content: TextField(
          controller: nameController,
          decoration: const InputDecoration(
            labelText: 'Session Name',
            hintText: 'e.g. fix-login-bug',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () {
              ref.read(sessionListProvider.notifier)
                .createSession(nameController.text, '');
              Navigator.pop(ctx);
            },
            child: const Text('Create'),
          ),
        ],
      ),
    );
  }
}
```

- [ ] **Step 4: Write ChatPage — core UI**

```dart
// lib/pages/chat_page.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/chat_provider.dart';

class ChatPage extends ConsumerStatefulWidget {
  final String sessionId;
  const ChatPage({super.key, required this.sessionId});

  @override
  ConsumerState<ChatPage> createState() => _ChatPageState();
}

class _ChatPageState extends ConsumerState<ChatPage> {
  final _textController = TextEditingController();
  final _scrollController = ScrollController();
  bool _isStreaming = false;

  @override
  void initState() {
    super.initState();
    // Connect WebSocket for this session
    ref.read(chatProviderFamily(widget.sessionId).notifier).connectAndListen();
  }

  @override
  void dispose() {
    _textController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _sendMessage() {
    final text = _textController.text.trim();
    if (text.isEmpty) return;
    _textController.clear();
    ref.read(chatProviderFamily(widget.sessionId).notifier).sendMessage(text);
    _scrollToBottom();
  }

  void _interrupt() {
    ref.read(chatProviderFamily(widget.sessionId).notifier).interrupt();
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final messages = ref.watch(chatProviderFamily(widget.sessionId));
    _isStreaming = messages.isNotEmpty && messages.last.isStreaming;

    return Scaffold(
      appBar: AppBar(
        title: Text('Session ${widget.sessionId}'),
        actions: [
          if (_isStreaming)
            IconButton(
              icon: const Icon(Icons.stop),
              onPressed: _interrupt,
            ),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: ListView.builder(
              controller: _scrollController,
              padding: const EdgeInsets.all(16),
              itemCount: messages.length,
              itemBuilder: (context, index) {
                return _buildMessageBubble(messages[index], context);
              },
            ),
          ),
          _buildInputBar(),
        ],
      ),
    );
  }

  Widget _buildMessageBubble(message, BuildContext context) {
    final isUser = message.role == 'user';
    return Align(
      alignment: isUser ? Alignment.centerRight : Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.only(bottom: 12),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: isUser
              ? Theme.of(context).colorScheme.primaryContainer
              : Theme.of(context).colorScheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
        ),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.8,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (final block in message.blocks)
              _buildBlock(block, context),
            if (message.isStreaming)
              const SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildBlock(block, BuildContext context) {
    switch (block.type) {
      case BlockType.markdown:
        return Text(block.content);
      case BlockType.code:
        return Container(
          width: double.infinity,
          padding: const EdgeInsets.all(8),
          decoration: BoxDecoration(
            color: Colors.black87,
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(
            block.content,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
          ),
        );
      case BlockType.error:
        return Text(
          block.content,
          style: const TextStyle(color: Colors.redAccent),
        );
      case BlockType.thinking:
        return Row(
          children: [
            const SizedBox(
              width: 12, height: 12,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(width: 8),
            Text(block.content, style: const TextStyle(fontStyle: FontStyle.italic)),
          ],
        );
      default:
        return Text(block.content);
    }
  }

  Widget _buildInputBar() {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerLow,
        border: Border(top: BorderSide(color: Theme.of(context).dividerColor)),
      ),
      child: SafeArea(
        child: Row(
          children: [
            Expanded(
              child: TextField(
                controller: _textController,
                decoration: const InputDecoration(
                  hintText: 'Type a message...',
                  border: OutlineInputBorder(),
                  contentPadding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                ),
                maxLines: 4,
                minLines: 1,
                textInputAction: TextInputAction.newline,
              ),
            ),
            const SizedBox(width: 8),
            IconButton.filled(
              icon: Icon(_isStreaming ? Icons.stop : Icons.send),
              onPressed: _isStreaming ? _interrupt : _sendMessage,
            ),
          ],
        ),
      ),
    );
  }
}
```

- [ ] **Step 5: Write SettingsPage placeholder**

```dart
// lib/pages/settings_page.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/connection_provider.dart';

class SettingsPage extends ConsumerWidget {
  const SettingsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final conn = ref.watch(connectionProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          ListTile(
            title: const Text('Bridge Host'),
            subtitle: Text(conn.host ?? 'Not connected'),
          ),
          ListTile(
            title: const Text('Status'),
            subtitle: Text(conn.status.name),
            trailing: Icon(
              conn.status == ConnectionStatus.paired
                  ? Icons.check_circle
                  : Icons.error,
              color: conn.status == ConnectionStatus.paired
                  ? Colors.green
                  : Colors.red,
            ),
          ),
          const Divider(),
          ListTile(
            title: const Text('Disconnect'),
            leading: const Icon(Icons.logout),
            onTap: () {
              ref.read(connectionProvider.notifier).disconnect();
              Navigator.of(context).pop();
            },
          ),
        ],
      ),
    );
  }
}
```

- [ ] **Step 6: Verify compilation**

Run: `cd phone_claude_app && flutter analyze`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add phone_claude_app/lib/pages/
git commit -m "feat: add all pages - splash, pairing, sessions, chat, settings"
```

---

## Phase 7: Flutter Session Management + File Browser

### Task 7.1: File browser bottom sheet

**Files:**
- Create: `phone_claude_app/lib/widgets/file_browser_sheet.dart`

- [ ] **Step 1: Write FileBrowserSheet**

```dart
// lib/widgets/file_browser_sheet.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/connection_provider.dart';

class FileBrowserSheet extends ConsumerStatefulWidget {
  final Function(String path) onFileSelected;

  const FileBrowserSheet({super.key, required this.onFileSelected});

  @override
  ConsumerState<FileBrowserSheet> createState() => _FileBrowserSheetState();
}

class _FileBrowserSheetState extends ConsumerState<FileBrowserSheet> {
  String _currentPath = '.';
  List<Map<String, dynamic>> _entries = [];
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _loadDirectory(_currentPath);
  }

  Future<void> _loadDirectory(String path) async {
    setState(() => _loading = true);
    final conn = ref.read(connectionProvider);
    if (conn.apiService == null) return;

    try {
      final entries = await conn.apiService!.listFiles(path);
      setState(() {
        _currentPath = path;
        _entries = entries;
      });
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to browse: $e')),
        );
      }
    }
    setState(() => _loading = false);
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      height: MediaQuery.of(context).size.height * 0.5,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                IconButton(
                  icon: const Icon(Icons.arrow_upward),
                  onPressed: () {
                    if (_currentPath != '.') {
                      final parent = _currentPath.contains('/')
                          ? _currentPath.substring(0, _currentPath.lastIndexOf('/'))
                          : '.';
                      _loadDirectory(parent);
                    }
                  },
                ),
                Expanded(
                  child: Text(
                    _currentPath,
                    style: Theme.of(context).textTheme.bodyMedium,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : ListView.builder(
                    itemCount: _entries.length,
                    itemBuilder: (context, index) {
                      final entry = _entries[index];
                      final name = entry['name'] as String;
                      final isDir = entry['is_dir'] as bool? ?? false;

                      return ListTile(
                        leading: Icon(
                          isDir ? Icons.folder : Icons.insert_drive_file,
                          color: isDir ? Colors.amber : Colors.grey,
                        ),
                        title: Text(name),
                        onTap: () {
                          if (isDir) {
                            final newPath = _currentPath == '.'
                                ? name
                                : '$_currentPath/$name';
                            _loadDirectory(newPath);
                          } else {
                            final filePath = _currentPath == '.'
                                ? name
                                : '$_currentPath/$name';
                            widget.onFileSelected(filePath);
                            Navigator.pop(context);
                          }
                        },
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}
```

- [ ] **Step 2: Update ChatPage to integrate file browser**

Add `@` button to the input bar in ChatPage. In `_buildInputBar()`, add before the TextField:

```dart
IconButton(
  icon: const Icon(Icons.folder_open),
  onPressed: () {
    showModalBottomSheet(
      context: context,
      builder: (ctx) => FileBrowserSheet(
        onFileSelected: (path) {
          final currentText = _textController.text;
          final cursorPos = _textController.selection.baseOffset;
          if (cursorPos >= 0) {
            final beforeCursor = currentText.substring(0, cursorPos);
            final afterCursor = currentText.substring(cursorPos);
            _textController.text = '$beforeCursor@$path $afterCursor';
            _textController.selection = TextSelection.collapsed(
              offset: cursorPos + path.length + 2,
            );
          } else {
            _textController.text = '${currentText}@$path ';
          }
        },
      ),
    );
  },
),
```

Add import: `import '../widgets/file_browser_sheet.dart';`

- [ ] **Step 3: Verify compilation**

Run: `cd phone_claude_app && flutter analyze`

- [ ] **Step 4: Commit**

```bash
git add phone_claude_app/lib/widgets/ phone_claude_app/lib/pages/chat_page.dart
git commit -m "feat: add file browser bottom sheet and integrate with chat"
```

---

## Phase 8: Push Notifications

### Task 8.1: FCM initialization in Flutter

**Files:**
- Modify: `phone_claude_app/lib/main.dart`
- Create: `phone_claude_app/lib/services/notification_service.dart`

- [ ] **Step 1: Write notification service**

```dart
// lib/services/notification_service.dart
import 'package:firebase_messaging/firebase_messaging.dart';

class NotificationService {
  static final NotificationService _instance = NotificationService._();
  factory NotificationService() => _instance;
  NotificationService._();

  final _fcm = FirebaseMessaging.instance;

  Future<void> initialize() async {
    // Request permission
    await _fcm.requestPermission(
      alert: true,
      badge: true,
      sound: true,
    );

    // Get FCM token
    final token = await _fcm.getToken();
    if (token != null) {
      // TODO: Register token with Go bridge via API
    }

    // Handle foreground messages
    FirebaseMessaging.onMessage.listen((RemoteMessage message) {
      // Show local notification
    });

    // Handle notification tap when app is in background
    FirebaseMessaging.onMessageOpenedApp.listen((RemoteMessage message) {
      // Navigate to session
      final sessionId = message.data['session_id'];
      if (sessionId != null) {
        // navigate to /chat/$sessionId
      }
    });
  }

  Future<String?> getToken() => _fcm.getToken();
}
```

- [ ] **Step 2: Initialize Firebase in main.dart**

Add to `main()` before `runApp`:
```dart
import 'package:firebase_core/firebase_core.dart';
import 'services/notification_service.dart';

// In main():
WidgetsFlutterBinding.ensureInitialized();
await Firebase.initializeApp();
await NotificationService().initialize();
```

- [ ] **Step 3: Verify compilation and commit**

Run: `cd phone_claude_app && flutter analyze`

```bash
git add phone_claude_app/lib/services/notification_service.dart phone_claude_app/lib/main.dart
git commit -m "feat: add FCM push notification service"
```

### Task 8.2: FCM push from Go bridge

**Files:**
- Create: `phone-claude-bridge/notify/fcm.go`

- [ ] **Step 1: Write FCM push wrapper**

```go
// notify/fcm.go
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type FCMSender struct {
	serverKey string
	client    *http.Client
}

type FCMMessage struct {
	To   string                 `json:"to"`
	Data map[string]interface{} `json:"data"`
	Notification *FCMNotification `json:"notification,omitempty"`
}

type FCMNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type FCMResponse struct {
	Success int `json:"success"`
	Failure int `json:"failure"`
}

func NewFCMSender(serverKey string) *FCMSender {
	return &FCMSender{
		serverKey: serverKey,
		client:    &http.Client{},
	}
}

func (f *FCMSender) Send(msg FCMMessage) error {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequest("POST",
		"https://fcm.googleapis.com/fcm/send",
		bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "key="+f.serverKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fcm send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var fcmResp FCMResponse
	json.Unmarshal(respBody, &fcmResp)

	if fcmResp.Failure > 0 {
		return fmt.Errorf("fcm partial failure: %s", string(respBody))
	}
	return nil
}

func (f *FCMSender) NotifySessionDone(sessionID, sessionName, token string) error {
	return f.Send(FCMMessage{
		To: token,
		Data: map[string]interface{}{
			"session_id": sessionID,
		},
		Notification: &FCMNotification{
			Title: "Claude Code",
			Body:  fmt.Sprintf("Session '%s' has completed", sessionName),
		},
	})
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd phone-claude-bridge && go build .`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add phone-claude-bridge/notify/fcm.go
git commit -m "feat: add FCM push notification sender"
```

---

## Phase 9: Integration + Polish

### Task 9.1: File browser implementation in Go

**Files:**
- Create: `phone-claude-bridge/filebrowser/browser.go`

- [ ] **Step 1: Write file browser**

```go
// filebrowser/browser.go
package filebrowser

import (
	"os"
	"sort"
)

type Entry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func List(path string) ([]Entry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		result = append(result, Entry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  size,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}
```

- [ ] **Step 2: Wire file browser into handler**

Update `handleFS` in `handler.go`:
```go
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

	entries, err := filebrowser.List(req.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"entries": entries})
}
```

Add imports: `"phone-claude-bridge/filebrowser"` and `"fmt"`.

- [ ] **Step 3: Verify compilation**

Run: `cd phone-claude-bridge && go build .`

- [ ] **Step 4: Commit**

```bash
git add phone-claude-bridge/filebrowser/ phone-claude-bridge/transport/handler.go
git commit -m "feat: implement file browser and wire into handlers"
```

### Task 9.2: Final integration test and polish

- [ ] **Step 1: Test Go bridge starts correctly**

Run: `cd phone-claude-bridge && .\phone-claude-bridge.exe`
Expected: Log messages showing listeners starting, no crashes

- [ ] **Step 2: Test health endpoint**

Run: `curl http://127.0.0.1:9527/api/v1/health`
Expected: `{"status":"ok","sessions":0}`

- [ ] **Step 3: Test pairing flow**

Run: `curl -X POST http://127.0.0.1:9527/api/v1/auth/pair -H "Content-Type: application/json" -d '{"device_name":"test"}'`
Expected: `{"pin":"xxxx","expires_in":"300","device_name":"test"}`

- [ ] **Step 4: Test session creation**

Run:
```bash
export TOKEN=$(curl -X POST http://127.0.0.1:9527/api/v1/auth/verify -H "Content-Type: application/json" -d '{"pin":"xxxx","device_name":"test"}' | jq -r '.token')
curl -X POST http://127.0.0.1:9527/api/v1/sessions -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"name":"test-session"}'
```
Expected: `{"id":"sess_xxxx","name":"test-session"}`

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat: complete integration - Go bridge + Flutter app ready for testing"
```
