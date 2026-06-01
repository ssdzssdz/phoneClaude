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

var generatedSecretOnce sync.Once

type PairingSession struct {
	PIN        string
	DeviceName string
	ExpiresAt  time.Time
}

type AuthHandler struct {
	mu              sync.Mutex
	pairingSessions map[string]*PairingSession
	cfg             *config.Config
}

func NewAuthHandler(cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		pairingSessions: make(map[string]*PairingSession),
		cfg:             cfg,
	}
}

func (h *AuthHandler) generatePIN() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("%04d", n.Int64()+1000)
}

var generatedSecret []byte

func (h *AuthHandler) getSecret() []byte {
	secret := h.cfg.Auth.JWTSecret
	if secret != "" {
		return []byte(secret)
	}
	generatedSecretOnce.Do(func() {
		b := make([]byte, 32)
		rand.Read(b)
		generatedSecret = []byte(fmt.Sprintf("%x", b))
	})
	return generatedSecret
}

func (h *AuthHandler) getJWTExpiry() time.Duration {
	d, err := time.ParseDuration(h.cfg.Auth.JWTTTL)
	if err != nil {
		d = 720 * time.Hour
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

	log.Printf("🔑 Pairing PIN: %s (device: %s, expires in 5 min)", pin, req.DeviceName)

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
