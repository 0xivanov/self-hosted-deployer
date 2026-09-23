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

	clicore "github.com/0xivanov/self-hosted-deployer/internal/cli"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
)

const maxRegistryCredentialFileSize = 16 << 10

type registryCredentialFile struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a cliApp) registry(args []string, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: deployer registry <create|list>")
		return 2
	}
	resolved, err := resolveRuntimeOptions(opts)
	if err != nil {
		fmt.Fprintln(a.stderr, "registry credential operation failed")
		return 1
	}
	client, closeClient, err := a.newVerifiedPlatformClient(resolved)
	if err != nil {
		fmt.Fprintln(a.stderr, "registry credential operation failed")
		return 1
	}
	defer closeClient()
	registryClient, ok := client.(registryCredentialClient)
	if !ok {
		fmt.Fprintln(a.stderr, "registry credential service is unavailable")
		return 1
	}
	switch args[0] {
	case "create":
		return a.registryCreate(args[1:], resolved, registryClient)
	case "list":
		return a.registryList(args[1:], resolved, registryClient)
	default:
		fmt.Fprintf(a.stderr, "unknown registry command %q\n", args[0])
		fmt.Fprintln(a.stderr, "usage: deployer registry <create|list>")
		return 2
	}
}

func (a cliApp) registryCreate(args []string, opts runtimeOptions, client registryCredentialClient) int {
	flags := flag.NewFlagSet("registry create", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	var credentialsPath string
	flags.StringVar(&credentialsPath, "credentials", "", "path to a credentials JSON file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 3 || strings.TrimSpace(credentialsPath) == "" {
		fmt.Fprintln(a.stderr, "usage: deployer registry create --credentials <owner-private JSON file> <app> <64hexrevision> <docker.io|ghcr.io>")
		return 2
	}
	appName, revision, registry := flags.Arg(0), flags.Arg(1), flags.Arg(2)
	if !registryauth.ValidAppName(appName) || !registryauth.ValidRevision(revision) || (registry != "docker.io" && registry != "ghcr.io") {
		fmt.Fprintln(a.stderr, "invalid registry credential target")
		return 2
	}
	credentials, err := readRegistryCredentialFile(credentialsPath)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	if (registryauth.Credential{AppName: appName, Revision: revision, Registry: registry, Username: credentials.Username, Password: credentials.Password}).Validate() != nil {
		fmt.Fprintln(a.stderr, "invalid registry login details")
		return 2
	}
	announceMutationTarget(a.stderr, opts)
	result, err := client.CreateRegistryCredential(context.Background(), appName, revision, registry, credentials.Username, credentials.Password)
	if err != nil {
		fmt.Fprintln(a.stderr, "registry credential creation failed")
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, result); err != nil {
			fmt.Fprintf(a.stderr, "render registry credential: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "Registry credential %s for %s created.\n", result.Revision, result.AppName)
	return 0
}

func (a cliApp) registryList(args []string, opts runtimeOptions, client registryCredentialClient) int {
	if len(args) != 1 || !registryauth.ValidAppName(args[0]) {
		fmt.Fprintln(a.stderr, "usage: deployer registry list <app>")
		return 2
	}
	credentials, err := client.ListRegistryCredentials(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if opts.output == clicore.OutputJSON {
		if err := clicore.RenderJSON(a.stdout, map[string]any{"credentials": credentials}); err != nil {
			fmt.Fprintf(a.stderr, "render registry credentials: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(a.stdout, "REVISION REGISTRY CREATED_AT")
	for _, credential := range credentials {
		fmt.Fprintf(a.stdout, "%s %s %s\n", credential.Revision, credential.Registry, credential.CreatedAt)
	}
	return 0
}

func readRegistryCredentialFile(path string) (registryCredentialFile, error) {
	path = filepath.Clean(path)
	lstat, err := os.Lstat(path)
	if err != nil {
		return registryCredentialFile{}, errors.New("cannot read credentials file")
	}
	if !lstat.Mode().IsRegular() || (lstat.Mode().Perm() != 0o600 && lstat.Mode().Perm() != 0o400) {
		return registryCredentialFile{}, errors.New("credentials file must be a regular owner-only 0600 or 0400 file")
	}
	if owner, ok := lstat.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Getuid() {
		return registryCredentialFile{}, errors.New("credentials file must be owned by the current user")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return registryCredentialFile{}, errors.New("cannot read credentials file")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !os.SameFile(lstat, stat) || !stat.Mode().IsRegular() || stat.Size() > maxRegistryCredentialFileSize || (stat.Mode().Perm() != 0600 && stat.Mode().Perm() != 0400) {
		return registryCredentialFile{}, errors.New("credentials file changed or is invalid")
	}
	if owner, ok := stat.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Getuid() {
		return registryCredentialFile{}, errors.New("credentials file ownership changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRegistryCredentialFileSize+1))
	if err != nil || len(data) > maxRegistryCredentialFileSize {
		return registryCredentialFile{}, errors.New("credentials file exceeds size limit or cannot be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var credentials registryCredentialFile
	if err := decoder.Decode(&credentials); err != nil {
		return registryCredentialFile{}, errors.New("invalid credentials file")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return registryCredentialFile{}, errors.New("invalid credentials file")
	}
	if credentials.Username == "" || credentials.Password == "" {
		return registryCredentialFile{}, errors.New("credentials file must contain username and password")
	}
	return credentials, nil
}
