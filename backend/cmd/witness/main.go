package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/witness"
)

func main() {
	listenAddr := strings.TrimSpace(os.Getenv("XIASS_WITNESS_LISTEN"))
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8091"
	}
	dataPath := strings.TrimSpace(os.Getenv("XIASS_WITNESS_DATA"))
	if dataPath == "" {
		dataPath = "/var/lib/xiass-witness/witness.db"
	}
	store, err := witness.Open(dataPath)
	if err != nil {
		log.Fatalf("open XIASS witness: %v", err)
	}
	defer func() { _ = store.Close() }()

	service, err := witness.NewServer(store, os.Getenv("XIASS_WITNESS_TOKEN"))
	if err != nil {
		log.Fatalf("configure XIASS witness: %v", err)
	}
	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("XIASS witness listening on %s", listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve XIASS witness: %v", err)
	}
}
