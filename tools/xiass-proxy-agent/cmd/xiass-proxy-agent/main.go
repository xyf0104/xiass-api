package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xyf0104/xiass-proxy-agent/internal/control"
)

const defaultListenAddress = "127.0.0.1:37941"

func main() {
	listen := flag.String("listen", envOr("XIASS_PROXY_AGENT_LISTEN", defaultListenAddress), "loopback control API address")
	token := flag.String("token", os.Getenv("XIASS_PROXY_AGENT_TOKEN"), "local control API bearer token")
	tokenFile := flag.String("token-file", os.Getenv("XIASS_PROXY_AGENT_TOKEN_FILE"), "file containing the local control API bearer token")
	flag.Parse()

	if !controlAddressIsLoopback(*listen) {
		fatal("-listen must be an explicit loopback address")
	}
	if strings.TrimSpace(*token) == "" && strings.TrimSpace(*tokenFile) != "" {
		data, err := os.ReadFile(strings.TrimSpace(*tokenFile))
		if err != nil {
			fatal("cannot read control API token file")
		}
		*token = strings.TrimSpace(string(data))
	}
	if strings.TrimSpace(*token) == "" {
		fatal("set XIASS_PROXY_AGENT_TOKEN or pass -token")
	}

	manager := control.NewManager()
	defer manager.Close()
	api, err := control.NewServer(*token, manager)
	if err != nil {
		fatal(err.Error())
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fatal("cannot listen on loopback control API")
	}
	defer listener.Close()

	server := &http.Server{Handler: api.Handler()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	fmt.Printf("XIASS proxy agent control API listening on %s\n", listener.Addr().String())
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		fatal("control API stopped unexpectedly")
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func controlAddressIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "xiass-proxy-agent:", message)
	os.Exit(1)
}
