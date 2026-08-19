package main

import (
	"log/slog"
	"os"

	"github.com/disillusioned-labs/audit/internal/app"
	"github.com/disillusioned-labs/audit/internal/config"
)

func main() {
	// Loaded here, not inside RunConsumer, so RunConsumer stays callable with a config built in code.
	cfg, err := config.Load()
	if err != nil {
		// The real logger is built from cfg, so this failure has to report
		// through the default one.
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	cfg.Service.Name += "-consumer"

	if err := app.RunConsumer(cfg); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
