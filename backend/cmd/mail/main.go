// cmd/mail is the Go mail-engine entrypoint.
//
// Modes:
//   - default: opens SQLite, wires the mock provider + sync engine, prints
//     engine health (smoke test).
//   - --serve: runs the JSON-lines IPC protocol on stdin/stdout for Tauri
//     (docs/ipc.md). Tauri spawns this process and re-delivers keychain
//     secrets per request; nothing is persisted to disk.
package main

import (
	"flag"
	"fmt"
	"os"

	"mail/backend/internal/account"
	"mail/backend/internal/auth"
	"mail/backend/internal/database"
	"mail/backend/internal/events"
	"mail/backend/internal/ipc"
	"mail/backend/internal/message"
	"mail/backend/internal/pgp"
	"mail/backend/internal/provider"
	"mail/backend/internal/queue"
	"mail/backend/internal/search"
	"mail/backend/internal/sync"
	"mail/backend/internal/thread"
)

func main() {
	dbPath := flag.String("db", "mail.db", "path to the local SQLite database")
	serve := flag.Bool("serve", false, "run the JSON-lines IPC server on stdio")
	flag.Parse()

	// MAIL_DB_KEY (hex) reaches the engine from Tauri via the spawn
	// environment: the key lives in the OS keychain, never on disk (see
	// src-tauri/src/secrets.rs). Empty key = plain SQLite (tests/smoke).
	db, err := database.Open(*dbPath, os.Getenv("MAIL_DB_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "database:", err)
		os.Exit(1)
	}
	defer db.Close()

	bus := events.New()
	engine := sync.New(db, provider.NewMock(), bus)
	secrets := auth.NewRuntimeSecrets()

	if *serve {
		app := &ipc.App{
			DB:       db,
			Accounts: account.New(db),
			Messages: message.New(db),
			Threads:  thread.New(db),
			Search:   search.New(db),
			Events:   bus,
			Engine:   engine,
			Secrets:  secrets,
			Watchers: sync.NewSupervisor(engine),
			Queue:    queue.New(db),
			PGP:      pgp.New(secrets, db),
		}
		if err := ipc.Serve(os.Stdin, os.Stdout, app); err != nil {
			fmt.Fprintln(os.Stderr, "ipc:", err)
			os.Exit(1)
		}
		return
	}

	// smoke mode: verify the engine + event stream work end to end
	ch, stop := bus.Subscribe()
	defer stop()
	go func() {
		for e := range ch {
			fmt.Printf("event %s %v\n", e.Kind, e.Data)
		}
	}()

	fmt.Printf("mail engine ready (db=%s, provider=mock)\n", *dbPath)
	if err := engine.SyncAccount("a1"); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
	}
	fmt.Println("sync ok")
}
