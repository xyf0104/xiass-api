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

const defaultXIASSAPIURL = "https://api.xiass.com"

func main() {
	siteURL := flag.String("site", defaultXIASSAPIURL, "XIASS API website URL")
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
	server := newLocalHTTPServer(helper.routes())

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

	_ = server.Shutdown(context.Background())
}

func newLocalHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// History snapshots and verified rollback can take longer on large Codex homes.
		WriteTimeout: 3 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
}
