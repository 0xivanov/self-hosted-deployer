package ingress

import (
	"context"
	"fmt"
	"sort"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// preflightHostingCapacity checks the concrete nodes and Pods visible to this
// runtime. It intentionally models a conservative rollout reservation, not
// the complete Kubernetes scheduler.
func (c *Controller) preflightHostingCapacity(ctx context.Context, cfg appconfig.Config) error {
	if cfg.Hosting == nil {
		return nil
	}
	if c.nodes == nil || c.capacityPods == nil {
		return fmt.Errorf("hosting profile v1 is unsupported by this runtime: node and Pod clients are required for capacity admission")
	}
	requirements, err := cfg.Hosting.ResourceRequirements()
	if err != nil {
		return err
	}
	requested := resourceVectorFromRequests(requirements.Requests)
	requested.pods = 1
	if requested.anyNegative() {
		return fmt.Errorf("hosting profile has invalid negative resource request")
	}
	// Reserve the configured ceiling plus one rolling-update surge Pod. This
	// can over-reject updates, but never treats an unmeasured surge as safe.
	surge := (cfg.Hosting.MaxReplicas + 3) / 4 // default rolling update rounds 25% up
	if cfg.Resilience.Mode == appconfig.ResilienceResilient {
		surge = 1
	}
	podCount := cfg.Hosting.MaxReplicas + surge

	nodeList, err := c.nodes.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Kubernetes nodes for hosting capacity: %w", err)
	}
	free := make(map[string]resourceVector)
	nodeNames := make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		if node.Spec.Unschedulable || !readyForScheduling(node) || hasBlockingTaint(node) || !matchesPlacement(node, cfg) {
			continue
		}
		capacity, known := resourceVectorFromList(node.Status.Allocatable)
		if !known {
			return fmt.Errorf("hosting capacity is unknown for eligible Kubernetes node %q", node.Name)
		}
		free[node.Name] = capacity
		nodeNames = append(nodeNames, node.Name)
	}
	sort.Strings(nodeNames)
	if len(nodeNames) == 0 {
		return fmt.Errorf("hosting profile has no ready schedulable eligible Kubernetes nodes")
	}

	pods, err := c.capacityPods.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Pods for hosting capacity: %w", err)
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if pod.Spec.NodeName == "" {
			return fmt.Errorf("hosting capacity is unknown: nonterminal Pod %q is not assigned to a node", pod.Namespace+"/"+pod.Name)
		}
		if remaining, ok := free[pod.Spec.NodeName]; ok {
			if pod.Spec.Resources != nil {
				return fmt.Errorf("hosting capacity is unknown: Pod %q uses unsupported pod-level resources", pod.Namespace+"/"+pod.Name)
			}
			used := podRequest(&pod)
			remaining.subtract(used)
			free[pod.Spec.NodeName] = remaining
		}
	}

	if cfg.Resilience.Mode == appconfig.ResilienceResilient && cfg.Hosting.MaxReplicas > len(nodeNames) {
		return fmt.Errorf("hosting profile requires %d eligible nodes for resilient placement, found %d", cfg.Hosting.MaxReplicas, len(nodeNames))
	}
	usedNodes := make(map[string]bool)
	for index := 0; index < podCount; index++ {
		chosen := ""
		for _, nodeName := range nodeNames {
			if cfg.Resilience.Mode == appconfig.ResilienceResilient && index < cfg.Hosting.MaxReplicas && usedNodes[nodeName] {
				continue
			}
			if free[nodeName].fits(requested) {
				chosen = nodeName
				break
			}
		}
		if chosen == "" {
			return fmt.Errorf("insufficient eligible Kubernetes capacity for hosting profile: cannot place Pod %d of %d (including rollout surge)", index+1, podCount)
		}
		remaining := free[chosen]
		remaining.subtract(requested)
		free[chosen] = remaining
		if cfg.Resilience.Mode == appconfig.ResilienceResilient && index < cfg.Hosting.MaxReplicas {
			usedNodes[chosen] = true
		}
	}
	return nil
}

type resourceVector struct {
	cpu, memory, ephemeral resource.Quantity
	pods                   int64
}

func resourceVectorFromList(values corev1.ResourceList) (resourceVector, bool) {
	cpu, cpuOK := values[corev1.ResourceCPU]
	memory, memoryOK := values[corev1.ResourceMemory]
	ephemeral, ephemeralOK := values[corev1.ResourceEphemeralStorage]
	pods, podsOK := values[corev1.ResourcePods]
	return resourceVector{cpu: cpu.DeepCopy(), memory: memory.DeepCopy(), ephemeral: ephemeral.DeepCopy(), pods: pods.Value()}, cpuOK && memoryOK && ephemeralOK && podsOK
}

func resourceVectorFromRequests(values corev1.ResourceList) resourceVector {
	return resourceVector{cpu: values[corev1.ResourceCPU].DeepCopy(), memory: values[corev1.ResourceMemory].DeepCopy(), ephemeral: values[corev1.ResourceEphemeralStorage].DeepCopy()}
}

func (r resourceVector) anyNegative() bool {
	return r.pods < 0 || r.cpu.Sign() < 0 || r.memory.Sign() < 0 || r.ephemeral.Sign() < 0
}

func (r resourceVector) fits(request resourceVector) bool {
	return r.pods >= request.pods && r.cpu.Cmp(request.cpu) >= 0 && r.memory.Cmp(request.memory) >= 0 && r.ephemeral.Cmp(request.ephemeral) >= 0
}

func (r *resourceVector) subtract(value resourceVector) {
	r.pods -= value.pods
	r.cpu.Sub(value.cpu)
	r.memory.Sub(value.memory)
	r.ephemeral.Sub(value.ephemeral)
}

func podRequest(pod *corev1.Pod) resourceVector {
	request := resourceVector{}
	for _, container := range pod.Spec.Containers {
		request.add(resourceVectorFromRequests(container.Resources.Requests))
	}
	// Sum every init request conservatively, including restartable sidecars.
	// This over-reserves sequential init containers rather than undercounting
	// sidecars that continue running with the application containers.
	for _, container := range pod.Spec.InitContainers {
		request.add(resourceVectorFromRequests(container.Resources.Requests))
	}
	request.add(resourceVectorFromRequests(pod.Spec.Overhead))
	request.pods = 1
	return request
}

func (r *resourceVector) add(value resourceVector) {
	r.pods += value.pods
	r.cpu.Add(value.cpu)
	r.memory.Add(value.memory)
	r.ephemeral.Add(value.ephemeral)
}

func (r *resourceVector) max(value resourceVector) {
	if value.cpu.Cmp(r.cpu) > 0 {
		r.cpu = value.cpu
	}
	if value.memory.Cmp(r.memory) > 0 {
		r.memory = value.memory
	}
	if value.ephemeral.Cmp(r.ephemeral) > 0 {
		r.ephemeral = value.ephemeral
	}
}
