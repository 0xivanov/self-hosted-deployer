package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/0xivanov/self-hosted-deployer/internal/backup"
)

func backupCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "backup requires create or restore")
		return 2
	}
	switch args[0] {
	case "create":
		return runBackupCreate(args[1:])
	case "restore":
		return runBackupRestore(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown backup command: %s\n", args[0])
		return 2
	}
}

func runBackupCreate(args []string) int {
	flags := flag.NewFlagSet("deployer-server backup create", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	databasePath := flags.String("database-path", "", "source SQLite database path")
	outputPath := flags.String("output", "", "encrypted backup output path")
	keyFile := flags.String("key-file", "", "raw 32-byte encryption key file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *databasePath == "" || *outputPath == "" || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "--database-path, --output, and --key-file are required")
		return 2
	}
	if err := backup.Create(context.Background(), backup.CreateOptions{DatabasePath: *databasePath, OutputPath: *outputPath, KeyFile: *keyFile}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "backup created: %s\n", *outputPath)
	return 0
}

func runBackupRestore(args []string) int {
	flags := flag.NewFlagSet("deployer-server backup restore", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputPath := flags.String("input", "", "encrypted backup input path")
	outputPath := flags.String("output", "", "new restored SQLite database path")
	keyFile := flags.String("key-file", "", "raw 32-byte encryption key file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *inputPath == "" || *outputPath == "" || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "--input, --output, and --key-file are required")
		return 2
	}
	if err := backup.Restore(context.Background(), backup.RestoreOptions{InputPath: *inputPath, OutputPath: *outputPath, KeyFile: *keyFile}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "backup restored: %s\n", *outputPath)
	return 0
}
