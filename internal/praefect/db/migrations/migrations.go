package migrations

import (
	"log"

	migrate "github.com/rubenv/sql-migrate"
)

var allMigrations []*migrate.Migration

func List() {
	for _, m := range allMigrations {
		log.Printf("migration: %s", m.Id)
	}
}
