package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templateFiles embed.FS

func loadTemplates() (*template.Template, error) {
	return template.New("canopy").ParseFS(templateFiles, "templates/*.html")
}

func main() {
	configPath := flag.String("config", "", "optional path to the Canopy inventory JSON")
	listenOverride := flag.String("listen", "", "override the inventory listen address")
	flag.Parse()

	var inventory Inventory
	if *configPath != "" {
		loaded, err := LoadInventory(*configPath)
		if err != nil {
			log.Fatalf("load inventory: %v", err)
		}
		inventory = loaded
	} else {
		// Try default canopy.json if it exists, otherwise start with zero config
		if fi, err := os.Stat("canopy.json"); err == nil && !fi.IsDir() {
			if loaded, err := LoadInventory("canopy.json"); err == nil {
				inventory = loaded
			}
		}
	}

	if *listenOverride != "" {
		inventory.Listen = *listenOverride
	}
	if inventory.Listen == "" {
		inventory.Listen = "127.0.0.1:8080"
	}
	if inventory.FleetIntervalSeconds <= 0 {
		inventory.FleetIntervalSeconds = defaultFleetIntervalSeconds
	}
	if inventory.SelectedIntervalSeconds <= 0 {
		inventory.SelectedIntervalSeconds = defaultSelectedIntervalSeconds
	}
	templates, err := loadTemplates()
	if err != nil {
		log.Fatalf("load templates: %v", err)
	}
	collector := NewCLICollector(refreshTimeout)
	app := NewApp(inventory, collector, templates)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Auto-discover local Forest instances and merge them alongside explicit
	// inventory entries, which reconciliation must preserve.
	discovered, err := DiscoverLocalInstances(ctx)
	if err == nil && len(discovered) > 0 {
		app.syncDiscoveredInstances(ctx, discovered)
	}

	app.Start(ctx)

	server := &http.Server{
		Addr:              inventory.Listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}
