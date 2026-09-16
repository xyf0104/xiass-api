package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "", "path to helper config")
	flag.Parse()
	command := "serve"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	if command == "config-path" {
		path, err := defaultConfigPath()
		fatalIf(err)
		fmt.Println(path)
		return
	}
	cfg, err := loadConfig(*configPath)
	fatalIf(err)
	switch command {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log.Printf("XIASS AdsPower helper listening on %s; callback on %s", cfg.ListenAddress, cfg.CallbackAddress)
		fatalIf(runServers(ctx, cfg))
	case "doctor":
		fatalIf(runDoctor(cfg))
	case "install":
		fatalIf(installResidentHelper(cfg))
		fmt.Println("XIASS 授权助手后台服务已安装")
	default:
		log.Fatalf("unknown command %q", command)
	}
}

func runDoctor(cfg *config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := newAdsPowerClient(cfg)
	if err := client.status(ctx); err != nil {
		return err
	}
	origins := make([]string, 0, len(cfg.Servers))
	for origin := range cfg.Servers {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	for _, origin := range origins {
		server := cfg.Servers[origin]
		profile, err := resolveAdsPowerTemplate(ctx, client, server)
		if err != nil {
			return fmt.Errorf("%s template: %w", origin, err)
		}
		exitIP, err := verifyProxyExitIP(ctx, profile.UserProxyConfig)
		if err != nil {
			return fmt.Errorf("%s proxy: %w", origin, err)
		}
		fmt.Printf("%s\t%s\t%s\n", server.EnvironmentKey, profile.UserID, exitIP)
	}
	return nil
}

func fatalIf(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
