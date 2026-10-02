package version

import (
	"fmt"
	"runtime/debug"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

func Current() Info {
	buildInfo, buildInfoOK := debug.ReadBuildInfo()
	return Info{
		Version:   Version,
		Commit:    verifiedCommit(Commit, buildInfo, buildInfoOK),
		BuildDate: BuildDate,
	}
}

func (i Info) String() string {
	return fmt.Sprintf("version=%s commit=%s build_date=%s", i.Version, i.Commit, i.BuildDate)
}

func verifiedCommit(embedded string, info *debug.BuildInfo, buildInfoOK bool) string {
	if !buildInfoOK || info == nil {
		return provenanceFailure(embedded, "unverified")
	}

	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["vcs"] != "git" || !validRevision(settings["vcs.revision"]) {
		return provenanceFailure(embedded, "unverified")
	}

	revision := settings["vcs.revision"]
	expected := revision
	switch settings["vcs.modified"] {
	case "true":
		expected += "-dirty"
	case "false":
	default:
		return provenanceFailure(embedded, "unverified")
	}
	if embedded != expected {
		return provenanceFailure(expected, "mismatch")
	}
	return expected
}

func validRevision(revision string) bool {
	if len(revision) != 40 && len(revision) != 64 {
		return false
	}
	for _, character := range revision {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func provenanceFailure(commit string, reason string) string {
	if commit == "" {
		commit = "unknown"
	}
	return commit + "-" + reason
}
