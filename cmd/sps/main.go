package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/superdata/superprojectsyncer/internal/app"
	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/service"
)

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
	case "approve":
		approveCmd(os.Args[2:])
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
  sps run [--config PATH]       Run sync daemon (foreground)
  sps install [--config PATH]   Install as OS service (admin/sudo)
  sps uninstall                 Remove OS service
  sps status [--config PATH]    Show sync groups and peers
  sps approve SYNC FOLDER       Approve folder in ask_folder mode

See config.example.toml and BUILD_PLAN.md for details.
`)
}

func configFlag(args []string) (string, []string) {
	fs := flag.NewFlagSet("cmd", flag.ExitOnError)
	cfg := fs.String("config", "config.toml", "path to config file")
	_ = fs.Parse(args)
	return *cfg, fs.Args()
}

func runCmd(args []string) {
	cfgPath, _ := configFlag(args)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatal(err)
	}

	application, err := app.New(cfg)
	if err != nil {
		fatal(err)
	}
	defer application.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := application.Start(ctx); err != nil {
		fatal(err)
	}

	log.Printf("running with %d sync group(s); peer_id=%s", len(cfg.Syncs), application.PeerID)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	cancel()
}

func installCmd(args []string) {
	cfgPath, _ := configFlag(args)
	exe, err := service.ExecutablePath()
	if err != nil {
		fatal(err)
	}
	if err := service.Install(exe, cfgPath); err != nil {
		fatal(err)
	}
	fmt.Println("Service installed and started.")
}

func statusCmd(args []string) {
	cfgPath, _ := configFlag(args)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatal(err)
	}
	application, err := app.New(cfg)
	if err != nil {
		fatal(err)
	}
	defer application.Close()
	fmt.Println(application.Status())
}

func approveCmd(args []string) {
	if len(args) < 2 {
		fatal(fmt.Errorf("usage: sps approve SYNC_NAME FOLDER"))
	}
	syncName, folder := args[0], args[1]
	cfgPath := "config.toml"
	if len(args) > 2 {
		cfgPath = args[2]
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
