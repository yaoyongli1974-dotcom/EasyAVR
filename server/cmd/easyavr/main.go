// Command easyavr starts the EasyAVR AI-native video fusion platform server.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/security"
	"github.com/easyavr/easyavr/internal/server"
	"github.com/easyavr/easyavr/internal/store"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("configuration: %v", err)
	}
	enc, err := security.New(cfg.CredentialKey, cfg.CredentialKeyID, cfg.CredentialKeyPrevious)
	if cfg.CredentialKey != "" {
		if err != nil {
			log.Fatalf("credential encryption key: %v", err)
		}
		security.SetDefault(enc)
	} else if cfg.IsProduction() {
		log.Fatal("configuration: EASYAVR_CREDENTIAL_KEY is required in production")
	}

	db, err := store.OpenWith(cfg.DBDriver, cfg.DatabaseDSN())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	if password, found, err := store.PersistedGBPassword(db); err != nil {
		log.Fatalf("load persisted GB SIP password: %v", err)
	} else if found {
		cfg.GB.Password = password
	}

	app := server.New(cfg, db)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("[easyavr] shutting down")
		app.Close()
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
	if err := app.Run(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
