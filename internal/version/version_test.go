package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVerifiedCommit(t *testing.T) {
	t.Parallel()

	sha1 := strings.Repeat("a", 40)
	sha256 := strings.Repeat("b", 64)
	tests := []struct {
		name      string
		embedded  string
		settings  []debug.BuildSetting
		buildInfo bool
		expected  string
	}{
		{
			name:      "clean exact sha1",
			embedded:  sha1,
			settings:  gitSettings(sha1, "false"),
			buildInfo: true,
			expected:  sha1,
		},
		{
			name:      "clean exact sha256",
			embedded:  sha256,
			settings:  gitSettings(sha256, "false"),
			buildInfo: true,
			expected:  sha256,
		},
		{
			name:      "dirty exact",
			embedded:  sha1 + "-dirty",
			settings:  gitSettings(sha1, "true"),
			buildInfo: true,
			expected:  sha1 + "-dirty",
		},
		{
			name:      "dirty cannot claim clean revision",
			embedded:  sha1,
			settings:  gitSettings(sha1, "true"),
			buildInfo: true,
			expected:  sha1 + "-dirty-mismatch",
		},
		{
			name:      "wrong embedded revision",
			embedded:  strings.Repeat("c", 40),
			settings:  gitSettings(sha1, "false"),
			buildInfo: true,
			expected:  sha1 + "-mismatch",
		},
		{
			name:      "missing build info",
			embedded:  sha1,
			buildInfo: false,
			expected:  sha1 + "-unverified",
		},
		{
			name:      "missing modified setting",
			embedded:  sha1,
			settings:  []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: sha1}},
			buildInfo: true,
			expected:  sha1 + "-unverified",
		},
		{
			name:      "invalid revision",
			embedded:  "unknown",
			settings:  gitSettings("ABC", "false"),
			buildInfo: true,
			expected:  "unknown-unverified",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			info := &debug.BuildInfo{Settings: test.settings}
			if got := verifiedCommit(test.embedded, info, test.buildInfo); got != test.expected {
				t.Fatalf("verifiedCommit() = %q, want %q", got, test.expected)
			}
		})
	}
}

func TestValidRevision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		revision string
		valid    bool
	}{
		{name: "sha1", revision: strings.Repeat("0", 40), valid: true},
		{name: "sha256", revision: strings.Repeat("f", 64), valid: true},
		{name: "short", revision: strings.Repeat("a", 39), valid: false},
		{name: "uppercase", revision: strings.Repeat("A", 40), valid: false},
		{name: "non hexadecimal", revision: strings.Repeat("g", 40), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validRevision(test.revision); got != test.valid {
				t.Fatalf("validRevision(%q) = %t, want %t", test.revision, got, test.valid)
			}
		})
	}
}

func gitSettings(revision string, modified string) []debug.BuildSetting {
	return []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: modified},
	}
}
