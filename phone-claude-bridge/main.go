package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"phone-claude-bridge/auth"
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

	ah := auth.NewAuthHandler(cfg)
	ah.CleanupExpiredPINs()

	handler := transport.NewHandler(sm, db, ah)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	authMux := ah.Middleware(mux)

	localServer := &http.Server{
		Addr:    cfg.Server.ListenLocal,
		Handler: authMux,
	}
	go func() {
		log.Printf("listening on %s (local)", cfg.Server.ListenLocal)
		if err := localServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("local server error: %v", err)
		}
	}()

	tsServer := &http.Server{
		Addr:    cfg.Server.ListenTailscale,
		Handler: authMux,
	}
	go func() {
		log.Printf("listening on %s (tailscale)", cfg.Server.ListenTailscale)
		if err := tsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("tailscale server error: %v", err)
		}
	}()

	log.Println("phone-claude-bridge is running")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	return nil
}
