// Package registryauth keeps image-pull credentials separate from application
// environment secrets. Callers must never log the credential or Docker config.
package registryauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid registry credential or image binding")
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var appPattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var repositoryPart = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)

// Credential is transient plaintext. Persistence must encrypt a separate payload
// that binds the app name, revision and registry along with the login details.
type Credential struct {
	AppName  string `json:"-"`
	Revision string `json:"-"`
	Registry string `json:"-"`
	Username string `json:"-"`
	Password string `json:"-"`
}

func (Credential) String() string   { return "[registry credential redacted]" }
func (Credential) GoString() string { return "[registry credential redacted]" }

func ValidRevision(s string) bool { return revisionPattern.MatchString(s) }
func ValidAppName(s string) bool  { return len(s) <= 63 && appPattern.MatchString(s) }

func (c Credential) Validate() error {
	if !ValidAppName(c.AppName) || !ValidRevision(c.Revision) || (c.Registry != "docker.io" && c.Registry != "ghcr.io") || len(c.Username) == 0 || len(c.Username) > 256 || len(c.Password) == 0 || len(c.Password) > 8192 || strings.Contains(c.Username, ":") {
		return ErrInvalid
	}
	for _, s := range []string{c.Username, c.Password} {
		if !utf8.ValidString(s) {
			return ErrInvalid
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// ValidateFor accepts only fully qualified digest pins on the credential's
// registry. A saved revision cannot be applied to another app or registry.
func (c Credential) ValidateFor(appName, revision, image string) error {
	if c.Validate() != nil || c.AppName != appName || c.Revision != revision || len(image) > 512 || !strings.HasPrefix(image, c.Registry+"/") {
		return ErrInvalid
	}
	name, digest, ok := strings.Cut(strings.TrimPrefix(image, c.Registry+"/"), "@sha256:")
	if !ok || !ValidRevision(digest) || len(name) > 255 {
		return ErrInvalid
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return ErrInvalid
	}
	for _, p := range parts {
		if !repositoryPart.MatchString(p) {
			return ErrInvalid
		}
	}
	return nil
}

func (c Credential) DockerConfigJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	host := c.Registry
	if host == "docker.io" {
		host = "https://index.docker.io/v1/"
	}
	// auth is sufficient for the standard Kubernetes Docker configuration reader.
	return json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))}}})
}
