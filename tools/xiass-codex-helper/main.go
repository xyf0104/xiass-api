package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	siteURL := flag.String("site", "", "XIASS API website URL")
	noBrowser := flag.Bool("no-browser", false, "do not open the browser automatically")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	codexHome, err := defaultCodexHome()
	if err != nil {
		log.Fatalf("resolve Codex home: %v", err)
	}
	state, err := randomState()
	if err != nil {
		log.Fatalf("generate local session state: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("start local helper: %v", err)
	}

	helper, err := newHelperServer(NewConfigManager(codexHome), *siteURL, state)
	if err != nil {
		_ = listener.Close()
		log.Fatalf("initialize helper: %v", err)
	}
	server := &http.Server{
		Handler:           helper.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("local helper server: %v", err)
			helper.requestShutdown()
		}
	}()

	localURL := "http://" + listener.Addr().String() + "/"
	if !*noBrowser {
		if err := openBrowser(localURL); err != nil {
			log.Printf("open browser: %v", err)
		}
	}
	log.Printf("XIASS Codex Helper %s is running at %s", version, localURL)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
	case <-helper.shutdown:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
