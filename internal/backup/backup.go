// Package backup provides an offline, encrypted SQLite snapshot format.
package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	keySize       = 32
	artifactMagic = "DEPLOYER-BACKUP-1\x00"
	maxArtifact   = 1 << 30
)

type CreateOptions struct {
	DatabasePath string
	OutputPath   string
	KeyFile      string
}

type RestoreOptions struct {
	InputPath  string
	OutputPath string
	KeyFile    string
}

func Create(ctx context.Context, opts CreateOptions) error {
	key, err := readKey(opts.KeyFile)
	if err != nil {
		return err
	}
	_, err = regularFile(opts.DatabasePath, "database")
	if err != nil {
		return err
	}
	if err := outputAvailable(opts.OutputPath); err != nil {
		return err
	}
	outputDir := filepath.Dir(opts.OutputPath)
	snapshot, err := os.CreateTemp(outputDir, ".deployer-backup-db-")
	if err != nil {
		return fmt.Errorf("create temporary database: %w", err)
	}
	snapshotPath := snapshot.Name()
	if err := snapshot.Close(); err != nil {
		os.Remove(snapshotPath)
		return fmt.Errorf("close temporary database: %w", err)
	}
	defer os.Remove(snapshotPath)

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(opts.DatabasePath))
	if err != nil {
		return fmt.Errorf("open database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("check database: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snapshotPath); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	if err := integrityCheck(ctx, snapshotPath); err != nil {
		return err
	}
	plain, err := readBoundedFile(snapshotPath, maxArtifact-64)
	if err != nil {
		return fmt.Errorf("read database snapshot: %w", err)
	}
	return writeArtifact(opts.OutputPath, key, plain)
}

func Restore(ctx context.Context, opts RestoreOptions) error {
	key, err := readKey(opts.KeyFile)
	if err != nil {
		return err
	}
	if err := outputAvailable(opts.OutputPath); err != nil {
		return err
	}
	artifact, err := readBoundedFile(opts.InputPath, maxArtifact)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	plain, err := decrypt(key, artifact)
	if err != nil {
		return err
	}
	outputDir := filepath.Dir(opts.OutputPath)
	tmp, err := os.CreateTemp(outputDir, ".deployer-restore-*")
	if err != nil {
		return fmt.Errorf("create restored database: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(plain); err != nil {
		tmp.Close()
		return fmt.Errorf("write restored database: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync restored database: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	if err := integrityCheck(ctx, tmpPath); err != nil {
		return err
	}
	if err := os.Link(tmpPath, opts.OutputPath); err != nil {
		return fmt.Errorf("publish restored database: %w", err)
	}
	return nil
}

func readKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("key file must be a regular file readable only by its owner")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open key file: %w", err)
	}
	defer f.Close()
	key, err := io.ReadAll(io.LimitReader(f, keySize+1))
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("key file must contain exactly %d raw bytes", keySize)
	}
	return key, nil
}

func regularFile(path, label string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", label)
	}
	return info, nil
}

func outputAvailable(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("output already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check output: %w", err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil {
		return fmt.Errorf("output directory: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("output directory is not a directory: %s", filepath.Dir(path))
	}
	return nil
}

func sqliteReadOnlyDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "file:/nonexistent-deployer-backup-invalid-path?mode=ro"
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro"}).String()
}

func newCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create encryption cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create authenticated cipher: %w", err)
	}
	return gcm, nil
}

func writeArtifact(path string, key, plain []byte) error {
	gcm, err := newCipher(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate backup nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, []byte(artifactMagic))
	f, err := os.CreateTemp(filepath.Dir(path), ".deployer-backup-*")
	if err != nil {
		return fmt.Errorf("create backup output: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	_, err = f.Write([]byte(artifactMagic))
	if err == nil {
		_, err = f.Write(nonce)
	}
	if err == nil {
		_, err = f.Write(sealed)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write backup output: %w", err)
	}
	if err := os.Link(tmpPath, path); err != nil {
		return fmt.Errorf("publish backup: %w", err)
	}
	return nil
}

func decrypt(key, artifact []byte) ([]byte, error) {
	gcm, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	if len(artifact) > maxArtifact || !bytes.HasPrefix(artifact, []byte(artifactMagic)) {
		return nil, errors.New("invalid backup format or corrupted backup")
	}
	offset := len(artifactMagic)
	if len(artifact) < offset+gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("invalid backup format or corrupted backup")
	}
	plain, err := gcm.Open(nil, artifact[offset:offset+gcm.NonceSize()], artifact[offset+gcm.NonceSize():], []byte(artifactMagic))
	if err != nil {
		return nil, errors.New("backup authentication failed")
	}
	return plain, nil
}

func integrityCheck(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("check restored database: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("restored database integrity check failed: %s", result)
	}
	return nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := regularFile(path, "backup input")
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, errors.New("backup input exceeds size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("backup input exceeds size limit")
	}
	return data, nil
}
