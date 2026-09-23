package registryauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCredentialRegistryBindingAndRedaction(t *testing.T) {
	c := Credential{AppName: "site-one", Revision: strings.Repeat("a", 64), Registry: "docker.io", Username: "user", Password: "private-value"}
	image := "docker.io/org/site@sha256:" + strings.Repeat("b", 64)
	if err := c.ValidateFor(c.AppName, c.Revision, image); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []struct{ app, revision, image string }{{"site-two", c.Revision, image}, {c.AppName, strings.Repeat("c", 64), image}, {c.AppName, c.Revision, "ghcr.io/org/site@sha256:" + strings.Repeat("b", 64)}, {c.AppName, c.Revision, "docker.io/org/site:latest"}, {c.AppName, c.Revision, "docker.io/../site@sha256:" + strings.Repeat("b", 64)}} {
		if c.ValidateFor(wrong.app, wrong.revision, wrong.image) == nil {
			t.Fatal("accepted incorrect binding")
		}
	}
	raw, err := c.DockerConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Auths) != 1 {
		t.Fatal("invalid docker config")
	}
	decoded, err := base64.StdEncoding.DecodeString(config.Auths["https://index.docker.io/v1/"].Auth)
	if err != nil || string(decoded) != "user:private-value" {
		t.Fatal("incorrect pull config")
	}
	public, _ := json.Marshal(c)
	for _, output := range []string{string(public), fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(output, c.Password) || strings.Contains(output, c.Username) {
			t.Fatal("credential leaked in default serialization")
		}
	}
	c.Password = "token\nwith-control"
	if c.Validate() == nil {
		t.Fatal("accepted control character")
	}
}
