package ingress

import (
	"errors"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
)

const DefaultNamespace = "deployer-apps"

const (
	DefaultClusterIssuer = "deployer-letsencrypt"
	DefaultACMEServer    = "https://acme-v02.api.letsencrypt.org/directory"

	traefikRetryMiddlewareAnnotation  = "traefik.ingress.kubernetes.io/router.middlewares"
	traefikServersTransportAnnotation = "traefik.ingress.kubernetes.io/service.serverstransport"
)

type TLSConfig struct {
	ACMEEmail     string
	ClusterIssuer string
	ACMEServer    string
}

func (c TLSConfig) WithDefaults() TLSConfig {
	c.ACMEEmail = strings.TrimSpace(c.ACMEEmail)
	c.ClusterIssuer = strings.TrimSpace(c.ClusterIssuer)
	c.ACMEServer = strings.TrimSpace(c.ACMEServer)
	if c.ClusterIssuer == "" {
		c.ClusterIssuer = DefaultClusterIssuer
	}
	if c.ACMEServer == "" {
		c.ACMEServer = DefaultACMEServer
	}
	return c
}

func (c TLSConfig) Enabled() bool {
	return strings.TrimSpace(c.ACMEEmail) != ""
}

type Manifest struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
}

type Metadata struct {
	Name        string            `json:"name" yaml:"name"`
	Namespace   string            `json:"namespace" yaml:"namespace"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type Spec struct {
	IngressClassName string `json:"ingressClassName,omitempty" yaml:"ingressClassName,omitempty"`
	TLS              []TLS  `json:"tls,omitempty" yaml:"tls,omitempty"`
	Rules            []Rule `json:"rules" yaml:"rules"`
}

type TLS struct {
	Hosts      []string `json:"hosts" yaml:"hosts"`
	SecretName string   `json:"secretName" yaml:"secretName"`
}

type Rule struct {
	Host string `json:"host" yaml:"host"`
	HTTP HTTP   `json:"http" yaml:"http"`
}

type HTTP struct {
	Paths []Path `json:"paths" yaml:"paths"`
}

type Path struct {
	Path     string  `json:"path" yaml:"path"`
	PathType string  `json:"pathType" yaml:"pathType"`
	Backend  Backend `json:"backend" yaml:"backend"`
}

type Backend struct {
	Service ServiceBackend `json:"service" yaml:"service"`
}

type ServiceBackend struct {
	Name string      `json:"name" yaml:"name"`
	Port ServicePort `json:"port" yaml:"port"`
}

type ServicePort struct {
	Number int `json:"number" yaml:"number"`
}

func ManifestForApp(cfg appconfig.Config, namespace string, tlsConfig TLSConfig) (Manifest, bool, error) {
	domain := strings.TrimSpace(cfg.Routing.Domain)
	if domain == "" {
		return Manifest{}, false, nil
	}
	if namespace == "" {
		namespace = DefaultNamespace
	}
	cfg.Normalize()
	if cfg.Name == "" {
		return Manifest{}, false, errors.New("app name is required")
	}
	if cfg.Service.Port < 1 || cfg.Service.Port > 65535 {
		return Manifest{}, false, errors.New("service port must be between 1 and 65535")
	}
	tlsConfig = tlsConfig.WithDefaults()
	manifest := Manifest{
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Metadata: Metadata{
			Name:      cfg.Name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": cfg.Name,
				managedByLabel:           managedByDeployer,
				"deployer.io/app":        cfg.Name,
			},
			Annotations: map[string]string{
				traefikRetryMiddlewareAnnotation: traefikResourceReference(namespace, cfg.Name),
			},
		},
		Spec: Spec{
			IngressClassName: "traefik",
			Rules: []Rule{{
				Host: domain,
				HTTP: HTTP{
					Paths: []Path{{
						Path:     "/",
						PathType: "Prefix",
						Backend: Backend{
							Service: ServiceBackend{
								Name: cfg.Name,
								Port: ServicePort{Number: cfg.Service.Port},
							},
						},
					}},
				},
			}},
		},
	}
	if tlsConfig.Enabled() {
		manifest.Metadata.Annotations["cert-manager.io/cluster-issuer"] = tlsConfig.ClusterIssuer
		manifest.Metadata.Annotations["traefik.ingress.kubernetes.io/router.entrypoints"] = "websecure"
		manifest.Metadata.Annotations["traefik.ingress.kubernetes.io/router.tls"] = "true"
		manifest.Spec.TLS = []TLS{{
			Hosts:      []string{domain},
			SecretName: cfg.Name + "-tls",
		}}
	}
	return manifest, true, nil
}

func traefikResourceReference(namespace string, name string) string {
	return namespace + "-" + name + "@kubernetescrd"
}
