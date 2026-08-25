package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseInitSchemaAndPing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_panel.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("Ping() failed: %v", err)
	}

	if err := db.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema() failed: %v", err)
	}

	// Verify tables created
	tables := []string{"servers", "users", "user_connections", "settings", "known_hosts", "backend_tunnels", "vpn_sessions"}
	for _, table := range tables {
		var name string
		err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s was not found in database: %v", table, err)
		}
	}

	// Verify default settings seeded
	var appSetting string
	err = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='appearance'").Scan(&appSetting)
	if err != nil {
		t.Fatalf("failed to query default setting 'appearance': %v", err)
	}
	if appSetting == "" {
		t.Errorf("expected appearance setting to be populated")
	}
}

func TestDatabaseConcurrentReadWrite(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_concurrency.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema() failed: %v", err)
	}

	// Concurrently write and read settings
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func(workerID int) {
			for j := 0; j < 20; j++ {
				_, err := db.ExecContext(ctx, "INSERT OR REPLACE INTO migration_flags (key, value) VALUES (?, ?)", "flag", "1")
				if err != nil {
					t.Errorf("worker %d write failed: %v", workerID, err)
				}
				var val string
				err = db.QueryRowContext(ctx, "SELECT value FROM migration_flags WHERE key=?", "flag").Scan(&val)
				if err != nil {
					t.Errorf("worker %d read failed: %v", workerID, err)
				}
			}
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}
