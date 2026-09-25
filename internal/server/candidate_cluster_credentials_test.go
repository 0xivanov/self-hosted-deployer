package server

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"syscall"
	"testing"
)

const maxCandidateCheckCredentialFileSize = 16 << 10

type candidateCheckCredentialFile struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// readCandidateCheckCredential is intentionally limited to the opt-in real
// cluster test. It keeps credential values out of test failures and logs.
func readCandidateCheckCredential(t *testing.T, path string) (username, password string) {
	t.Helper()

	lstat, err := os.Lstat(path)
	if err != nil {
		t.Fatal("cannot read candidate qualification credential file")
	}
	if !lstat.Mode().IsRegular() || (lstat.Mode().Perm() != 0o600 && lstat.Mode().Perm() != 0o400) {
		t.Fatal("candidate qualification credential file must be a regular owner-only 0600 or 0400 file")
	}
	owner, ok := lstat.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		t.Fatal("candidate qualification credential file must be owned by the current user")
	}

	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("cannot read candidate qualification credential file")
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || !os.SameFile(lstat, stat) || !stat.Mode().IsRegular() || stat.Size() > maxCandidateCheckCredentialFileSize || (stat.Mode().Perm() != 0o600 && stat.Mode().Perm() != 0o400) {
		t.Fatal("candidate qualification credential file changed or is invalid")
	}
	owner, ok = stat.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		t.Fatal("candidate qualification credential file ownership changed")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCandidateCheckCredentialFileSize+1))
	if err != nil || len(data) > maxCandidateCheckCredentialFileSize {
		t.Fatal("candidate qualification credential file exceeds size limit or cannot be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var credentials candidateCheckCredentialFile
	if err := decoder.Decode(&credentials); err != nil {
		t.Fatal("invalid candidate qualification credential file")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatal("invalid candidate qualification credential file")
	}
	if credentials.Username == "" || credentials.Password == "" {
		t.Fatal("candidate qualification credential file must contain username and password")
	}
	return credentials.Username, credentials.Password
}
