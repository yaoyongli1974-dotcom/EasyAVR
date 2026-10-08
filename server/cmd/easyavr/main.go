// Command easyavr starts the EasyAVR AI-native video fusion platform server.
package main

import (
	"log"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/server"
	"github.com/easyavr/easyavr/internal/store"
)

func main() {
	cfg := config.Load()

	db, err := store.OpenWith(cfg.DBDriver, cfg.DatabaseDSN())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("seed database: %v", err)
	}

	app := server.New(cfg, db)
	if err := app.Run(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
