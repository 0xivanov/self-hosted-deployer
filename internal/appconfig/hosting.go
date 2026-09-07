package appconfig

import (
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// HostingConfig opts an application into the managed hosting security and
// resource profile. A nil profile preserves the legacy rendering contract.
type HostingConfig struct {
	Version     string           `json:"version" yaml:"version"`
	Resources   HostingResources `json:"resources" yaml:"resources"`
	MaxReplicas int              `json:"maxReplicas" yaml:"maxReplicas"`
	Network     HostingNetwork   `json:"network" yaml:"network"`
}

type HostingResources struct {
	Requests ResourceQuantities `json:"requests" yaml:"requests"`
	Limits   ResourceQuantities `json:"limits" yaml:"limits"`
}

type ResourceQuantities struct {
	CPU              string `json:"cpu" yaml:"cpu"`
	Memory           string `json:"memory" yaml:"memory"`
	EphemeralStorage string `json:"ephemeralStorage" yaml:"ephemeralStorage"`
}

type HostingNetwork struct {
	Egress []HostingEgressRule `json:"egress,omitempty" yaml:"egress,omitempty"`
}

type HostingEgressRule struct {
	CIDR  string `json:"cidr" yaml:"cidr"`
	Ports []int  `json:"ports" yaml:"ports"`
}

func (c *HostingConfig) Validate(replicas int) error {
	if c == nil {
		return nil
	}
	if c.Version != "v1" {
		return fmt.Errorf("hosting.version must be v1")
	}
	if c.MaxReplicas < 1 || c.MaxReplicas > 1000 {
		return fmt.Errorf("hosting.maxReplicas must be between 1 and 1000")
	}
	if replicas > c.MaxReplicas {
		return fmt.Errorf("deploy.replicas %d exceeds hosting.maxReplicas %d", replicas, c.MaxReplicas)
	}
	requests, err := parseResourceQuantities(c.Resources.Requests, "hosting.resources.requests")
	if err != nil {
		return err
	}
	limits, err := parseResourceQuantities(c.Resources.Limits, "hosting.resources.limits")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name string
		key  corev1.ResourceName
	}{
		{"cpu", corev1.ResourceCPU},
		{"memory", corev1.ResourceMemory},
		{"ephemeralStorage", corev1.ResourceEphemeralStorage},
	} {
		limit := limits[field.key]
		request := requests[field.key]
		if limit.Cmp(request) < 0 {
			return fmt.Errorf("hosting.resources.limits.%s must be at least the request", field.name)
		}
	}
	for index, rule := range c.Network.Egress {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(rule.CIDR)); err != nil {
			return fmt.Errorf("hosting.network.egress[%d].cidr is invalid: %w", index, err)
		}
		if len(rule.Ports) == 0 {
			return fmt.Errorf("hosting.network.egress[%d].ports must not be empty", index)
		}
		for portIndex, port := range rule.Ports {
			if port < 1 || port > 65535 {
				return fmt.Errorf("hosting.network.egress[%d].ports[%d] must be between 1 and 65535", index, portIndex)
			}
		}
	}
	return nil
}

func parseResourceQuantities(values ResourceQuantities, prefix string) (corev1.ResourceList, error) {
	result := corev1.ResourceList{}
	fields := []struct {
		name  string
		value string
		key   corev1.ResourceName
	}{
		{"cpu", values.CPU, corev1.ResourceCPU},
		{"memory", values.Memory, corev1.ResourceMemory},
		{"ephemeralStorage", values.EphemeralStorage, corev1.ResourceEphemeralStorage},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return nil, fmt.Errorf("%s.%s is required", prefix, field.name)
		}
		parsed, err := resource.ParseQuantity(field.value)
		if err != nil {
			return nil, fmt.Errorf("%s.%s is invalid: %w", prefix, field.name, err)
		}
		if parsed.Sign() <= 0 {
			return nil, fmt.Errorf("%s.%s must be greater than zero", prefix, field.name)
		}
		result[field.key] = parsed
	}
	return result, nil
}

func (c *HostingConfig) ResourceRequirements() (corev1.ResourceRequirements, error) {
	if c == nil {
		return corev1.ResourceRequirements{}, nil
	}
	requests, err := parseResourceQuantities(c.Resources.Requests, "hosting.resources.requests")
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	limits, err := parseResourceQuantities(c.Resources.Limits, "hosting.resources.limits")
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	return corev1.ResourceRequirements{Requests: requests, Limits: limits}, nil
}
