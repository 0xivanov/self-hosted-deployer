package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCreateRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	key := filepath.Join(dir, "key")
	artifact := filepath.Join(dir, "backup.bin")
	restored := filepath.Join(dir, "restored.db")
	writeKey(t, key, []byte(strings.Repeat("k", keySize)))
	createDatabase(t, source)
	before := databaseSchema(t, source)

	if err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key}); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: restored, KeyFile: key}); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	checkDatabase(t, restored)
	if after := databaseSchema(t, source); after != before {
		t.Fatalf("source schema changed: before=%q after=%q", before, after)
	}
	if mode := fileMode(t, artifact); mode != 0o600 {
		t.Fatalf("backup mode = %o, want 600", mode)
	}
}

func TestBackupRejectsUnsafeOperations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(t *testing.T, dir, source, key, artifact string)
	}{
		{name: "missing source", run: func(t *testing.T, dir, source, key, artifact string) {
			err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key})
			if err == nil {
				t.Fatal("expected missing source error")
			}
		}},
		{name: "existing output", run: func(t *testing.T, dir, source, key, artifact string) {
			createDatabase(t, source)
			if err := os.WriteFile(artifact, []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key})
			if err == nil {
				t.Fatal("expected overwrite refusal")
			}
		}},
		{name: "wrong key", run: func(t *testing.T, dir, source, key, artifact string) {
			createDatabase(t, source)
			if err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key}); err != nil {
				t.Fatal(err)
			}
			wrong := filepath.Join(dir, "wrong-key")
			writeKey(t, wrong, []byte(strings.Repeat("x", keySize)))
			destination := filepath.Join(dir, "wrong.db")
			if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: destination, KeyFile: wrong}); err == nil {
				t.Fatal("expected wrong key error")
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrong-key restore created output: %v", err)
			}
		}},
		{name: "corrupt artifact", run: func(t *testing.T, dir, source, key, artifact string) {
			createDatabase(t, source)
			if err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			data[len(data)-1] ^= 1
			if err := os.WriteFile(artifact, data, 0o600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "corrupt.db")
			if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: destination, KeyFile: key}); err == nil {
				t.Fatal("expected corruption error")
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("corrupt restore created output: %v", err)
			}
		}},
		{name: "insecure key permissions", run: func(t *testing.T, dir, source, key, artifact string) {
			createDatabase(t, source)
			if err := os.Chmod(key, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Create(context.Background(), CreateOptions{DatabasePath: source, OutputPath: artifact, KeyFile: key}); err == nil {
				t.Fatal("expected key permission error")
			}
		}},
		{name: "invalid sqlite payload", run: func(t *testing.T, dir, source, key, artifact string) {
			if err := writeArtifact(artifact, []byte(strings.Repeat("k", keySize)), []byte("not sqlite")); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "invalid.db")
			if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: destination, KeyFile: key}); err == nil {
				t.Fatal("expected integrity error")
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid restore created output: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			key := filepath.Join(dir, "key")
			source := filepath.Join(dir, "source.db")
			artifact := filepath.Join(dir, "backup.bin")
			writeKey(t, key, []byte(strings.Repeat("k", keySize)))
			tt.run(t, dir, source, key, artifact)
		})
	}
}

func createDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO items(value) VALUES ('one');`); err != nil {
		t.Fatal(err)
	}
}

func checkDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM items WHERE id = 1").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "one" {
		t.Fatalf("restored value = %q", value)
	}
}

func databaseSchema(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var schema string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'items'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func writeKey(t *testing.T, path string, key []byte) {
	t.Helper()
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestBackupEscapesSQLitePathAndRejectsOversizedInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	createDatabase(t, source)
	unusual := filepath.Join(dir, "source?mode=rw#fragment.db")
	if err := os.Rename(source, unusual); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key")
	writeKey(t, key, []byte(strings.Repeat("k", keySize)))
	artifact := filepath.Join(dir, "backup.bin")
	if err := Create(context.Background(), CreateOptions{DatabasePath: unusual, OutputPath: artifact, KeyFile: key}); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.db")
	if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: restored, KeyFile: key}); err != nil {
		t.Fatal(err)
	}
	checkDatabase(t, restored)
	if err := os.Truncate(artifact, maxArtifact+1); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), RestoreOptions{InputPath: artifact, OutputPath: filepath.Join(dir, "too-big.db"), KeyFile: key}); err == nil {
		t.Fatal("oversized backup accepted")
	}
}
