package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
)

const maxEnvironmentValuesFileSize = 256 << 10

func (a cliApp) environment(args []string, opts cliOptions) int {
	if len(args) != 0 && args[0] == "create" {
		return a.environmentCreate(args[1:], opts)
	}
	fmt.Fprintln(a.stderr, "usage: deployer environment create --values <owner-private JSON file> <app> <64hexrevision>")
	return 2
}

func (a cliApp) environmentCreate(args []string, opts cliOptions) int {
	flags := flag.NewFlagSet("environment create", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var path string
	flags.StringVar(&path, "values", "", "path to environment values JSON file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 || strings.TrimSpace(path) == "" || !appconfig.ValidAppName(flags.Arg(0)) || !registryauth.ValidRevision(flags.Arg(1)) {
		fmt.Fprintln(a.stderr, "invalid environment bundle target")
		return 2
	}
	values, err := readEnvironmentValuesFile(path)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	resolved, err := resolveRuntimeOptions(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	client, closeClient, err := a.newVerifiedPlatformClient(resolved)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	defer closeClient()
	bundles, ok := client.(environmentBundleClient)
	if !ok {
		fmt.Fprintln(a.stderr, "environment service is unavailable")
		return 1
	}
	announceMutationTarget(a.stderr, resolved)
	result, err := bundles.CreateEnvironmentBundle(context.Background(), flags.Arg(0), flags.Arg(1), values)
	if err != nil {
		fmt.Fprintln(a.stderr, "environment bundle operation failed")
		return 1
	}
	if resolved.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render environment bundle: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "Environment revision %s for %s created.\n", result.Revision, result.AppName)
	return 0
}

func readEnvironmentValuesFile(path string) (map[string]string, error) {
	path = filepath.Clean(path)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("cannot read environment values file")
	}
	if !before.Mode().IsRegular() || (before.Mode().Perm() != 0600 && before.Mode().Perm() != 0400) {
		return nil, errors.New("environment values file must be a regular owner-only 0600 or 0400 file")
	}
	if owner, ok := before.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Getuid() {
		return nil, errors.New("environment values file must be owned by the current user")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("cannot read environment values file")
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("cannot read environment values file")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || (after.Mode().Perm() != 0600 && after.Mode().Perm() != 0400) || after.Size() > maxEnvironmentValuesFileSize {
		return nil, errors.New("environment values file changed or is invalid")
	}
	owner, ok := after.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		return nil, errors.New("environment values file changed or is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxEnvironmentValuesFileSize+1))
	if err != nil || len(raw) > maxEnvironmentValuesFileSize {
		return nil, errors.New("invalid environment values file")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	values, err := decodeEnvironmentValues(decoder)
	if err != nil {
		return nil, errors.New("invalid environment values file")
	}
	if len(values) > 64 {
		return nil, errors.New("invalid environment values")
	}
	total := 0
	for name, value := range values {
		if appconfig.ValidateSecretName(name) != nil || len(name) > 128 || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || len(value) > 8192 {
			return nil, errors.New("invalid environment values")
		}
		total += len(name) + len(value)
		if total > 32768 {
			return nil, errors.New("invalid environment values")
		}
	}
	return values, nil
}

func decodeEnvironmentValues(decoder *json.Decoder) (map[string]string, error) {
	values := map[string]string{}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("object required")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("string key required")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("duplicate key")
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return values, nil
}
