package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/superdata/superprojectsyncer/internal/app"
	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/logging"
	"github.com/superdata/superprojectsyncer/internal/relay"
	"github.com/superdata/superprojectsyncer/internal/service"
)

var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("sps: ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "install":
		installCmd(os.Args[2:])
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fatal(err)
		}
		fmt.Println("Service uninstalled.")
	case "status":
		statusCmd(os.Args[2:])
	case "watch":
		watchCmd(os.Args[2:])
	case "logs":
		logsCmd(os.Args[2:])
	case "approve":
		approveCmd(os.Args[2:])
	case "relay":
		relayCmd(os.Args[2:])
	case "version", "-V", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `SuperProjectSyncer (sps) — P2P folder sync

Usage:
  sps run [--config PATH]         Run sync daemon (foreground or service)
  sps install [--config PATH]     Install as OS service (admin/sudo)
  sps uninstall                   Remove OS service
  sps status [--config PATH] [-v] Show sync status (reads live state DB)
  sps watch [--config PATH] [-v]  Refresh status every 2s (Ctrl+C to stop)
  sps logs [--config PATH] [-n N] Show last N lines of the log file
  sps approve SYNC FOLDER         Approve folder in ask_folder mode
  sps relay run [--config PATH]   Run relay server (public VPS hub for NAT peers)
  sps relay status [--config PATH]
  sps version                     Print version

Logs default to <data_dir>/sps.log (set global.log_file in config).

See config.example.toml and BUILD_PLAN.md for details.
`)
}

type cmdFlags struct {
	config  string
	verbose bool
	lines   int
}

func parseCmdFlags(args []string) cmdFlags {
	fs := flag.NewFlagSet("cmd", flag.ExitOnError)
	cfg := fs.String("config", "config.toml", "path to config file")
	verbose := fs.Bool("v", false, "verbose output (files, transfers, activity)")
	lines := fs.Int("n", 50, "number of log lines to show")
	_ = fs.Parse(args)
	return cmdFlags{config: *cfg, verbose: *verbose, lines: *lines}
}

func loadConfig(path string) *config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	return cfg
}

func runCmd(args []string) {
	flags := parseCmdFlags(args)

	runDaemon := func(ctx context.Context) error {
		cfg := loadConfig(flags.config)
		logCloser, err := logging.Setup(cfg.Global.DataDir, cfg.Global.LogFile)
		if err != nil {
			return err
		}
		defer logCloser.Close()

		application, err := app.New(cfg)
		if err != nil {
			return err
		}
		defer application.Close()

		if err := application.Start(ctx); err != nil {
			return err
		}
		log.Printf("running with %d sync group(s); peer_id=%s", len(cfg.Syncs), application.PeerID)
		<-ctx.Done()
		log.Println("shutting down...")
		return nil
	}

	if service.RunIfService(runDaemon) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("signal: %v", s)
		cancel()
	case err := <-done:
		if err != nil {
			fatal(err)
		}
		return
	}
	if err := <-done; err != nil {
		fatal(err)
	}
}

func installCmd(args []string) {
	flags := parseCmdFlags(args)
	exe, err := service.ExecutablePath()
	if err != nil {
		fatal(err)
	}
	if err := service.Install(exe, flags.config); err != nil {
		fatal(err)
	}
	fmt.Println("Service installed and started.")
}

func statusCmd(args []string) {
	flags := parseCmdFlags(args)
	cfg := loadConfig(flags.config)
	out, err := app.StatusFromConfig(cfg, flags.verbose)
	if err != nil {
		fatal(err)
	}
	fmt.Println(out)
}

func watchCmd(args []string) {
	flags := parseCmdFlags(args)
	cfg := loadConfig(flags.config)
	interval := 2 * time.Second

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	printWatch := func() {
		fmt.Print("\033[2J\033[H")
		fmt.Printf("SuperProjectSyncer watch — refreshing every %s (Ctrl+C to stop)\n\n", interval)
		out, err := app.StatusFromConfig(cfg, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return
		}
		fmt.Println(out)
	}
	printWatch()

	for {
		select {
		case <-sig:
			return
		case <-ticker.C:
			printWatch()
		}
	}
}

func logsCmd(args []string) {
	flags := parseCmdFlags(args)
	cfg := loadConfig(flags.config)
	path := logging.ResolvePath(cfg.Global.DataDir, cfg.Global.LogFile)
	if path == "" {
		fmt.Println("file logging disabled (log_file = none)")
		return
	}
	out, err := logging.Tail(path, flags.lines)
	if err != nil {
		fatal(err)
	}
	fmt.Println(out)
}

func approveCmd(args []string) {
	if len(args) < 2 {
		fatal(fmt.Errorf("usage: sps approve SYNC_NAME FOLDER [--config PATH]"))
	}
	syncName, folder := args[0], args[1]
	cfgPath := "config.toml"
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatal(err)
	}
	application, err := app.New(cfg)
	if err != nil {
		fatal(err)
	}
	defer application.Close()
	if err := application.Approve(syncName, folder); err != nil {
		fatal(err)
	}
	fmt.Printf("Approved folder %q for sync %q\n", folder, syncName)
}

func fatal(err error) {
	log.Fatal(err)
}

func relayCmd(args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: sps relay run|status [--config PATH]"))
	}
	switch args[0] {
	case "run":
		relayRunCmd(args[1:])
	case "status":
		relayStatusCmd(args[1:])
	default:
		fatal(fmt.Errorf("unknown relay command: %s", args[0]))
	}
}

func relayRunCmd(args []string) {
	fs := flag.NewFlagSet("relay run", flag.ExitOnError)
	cfgPath := fs.String("config", "relay.toml", "relay server config")
	listen := fs.String("listen", "", "listen address (overrides config)")
	key := fs.String("key", "", "relay auth key (overrides config)")
	logFile := fs.String("log-file", "", "log file path")
	_ = fs.Parse(args)

	var cfg config.RelayServerConfig
	if *listen != "" && *key != "" {
		cfg = config.RelayServerConfig{Listen: *listen, Key: *key, LogFile: *logFile}
	} else {
		loaded, err := config.LoadRelayServer(*cfgPath)
		if err != nil {
			fatal(err)
		}
		cfg = *loaded
		if *listen != "" {
			cfg.Listen = *listen
		}
		if *key != "" {
			cfg.Key = *key
		}
		if *logFile != "" {
			cfg.LogFile = *logFile
		}
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:7750"
	}
	if cfg.Key == "" {
		fatal(fmt.Errorf("relay key required (--key or key in config)"))
	}

	logDir := os.TempDir()
	if cfg.LogFile != "" && !strings.EqualFold(cfg.LogFile, "none") {
		lf := cfg.LogFile
		if !filepath.IsAbs(lf) {
			lf = filepath.Join(logDir, lf)
		}
		logCloser, err := logging.Setup(logDir, lf)
		if err != nil {
			fatal(err)
		}
		defer logCloser.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	srv := relay.NewServer(relay.ServerConfig{Listen: cfg.Listen, Key: cfg.Key})
	log.Printf("relay server starting on %s", cfg.Listen)
	if err := srv.Run(ctx); err != nil {
		fatal(err)
	}
}

func relayStatusCmd(args []string) {
	fs := flag.NewFlagSet("relay status", flag.ExitOnError)
	cfgPath := fs.String("config", "relay.toml", "relay server config")
	_ = fs.Parse(args)
	cfg, err := config.LoadRelayServer(*cfgPath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("relay config: listen=%s key=***\n", cfg.Listen)
	fmt.Println("(Run 'sps relay run' to start; open this port on your VPS firewall)")
}
