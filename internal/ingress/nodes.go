package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) ensureSchedulableWorker(ctx context.Context, cfg appconfig.Config) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	nodes, err := c.nodes.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Kubernetes nodes for placement: %w", err)
	}
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable || !readyForScheduling(node) || !matchesPlacement(node, cfg) {
			continue
		}
		return nil
	}
	return fmt.Errorf("no ready schedulable Kubernetes worker matches placement architecture %q", cfg.Placement.Arch)
}

func (c *Controller) SyncNodeLabels(ctx context.Context, platformNode domain.Node) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	node, err := c.nodes.Get(ctx, platformNode.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Kubernetes Node %q for labels: %w", platformNode.Name, err)
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(platformNode.LabelsJSON), &labels); err != nil {
		return fmt.Errorf("decode node labels: %w", err)
	}
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	changed := setLabel(node.Labels, "deployer.io/node-id", platformNode.ID)
	for _, key := range []string{"location", "role"} {
		value, ok := labels[key]
		if ok && strings.TrimSpace(value) != "" {
			changed = setLabel(node.Labels, "deployer.io/"+key, strings.TrimSpace(value)) || changed
		} else if _, ok := node.Labels["deployer.io/"+key]; ok {
			delete(node.Labels, "deployer.io/"+key)
			changed = true
		}
	}
	if arch := placementArchitecture(platformNode.Arch); arch != "" {
		changed = setLabel(node.Labels, "kubernetes.io/arch", arch) || changed
	}
	if !changed {
		return nil
	}
	if _, err := c.nodes.Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Kubernetes Node %q labels: %w", platformNode.Name, err)
	}
	return nil
}

func setLabel(labels map[string]string, key string, value string) bool {
	if labels[key] == value {
		return false
	}
	labels[key] = value
	return true
}

func readyForScheduling(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func matchesPlacement(node corev1.Node, cfg appconfig.Config) bool {
	if arch := placementArchitecture(cfg.Placement.Arch); arch != "" && node.Labels["kubernetes.io/arch"] != arch {
		return false
	}
	if cfg.Resilience.Mode != appconfig.ResiliencePinned || len(cfg.Placement.Prefer) != 1 {
		return true
	}
	for key, value := range cfg.Placement.Prefer[0] {
		if node.Labels[placementLabelKey(key)] != value {
			return false
		}
	}
	return true
}

func (c *Controller) NodeReadiness(ctx context.Context, nodeName string) (string, string, bool, error) {
	if c.nodes == nil {
		return "unknown", "Kubernetes node client is not configured", false, nil
	}
	node, err := c.nodes.Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "missing", "Kubernetes node was not found", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("get Kubernetes Node %q: %w", nodeName, err)
	}
	schedulable := !node.Spec.Unschedulable
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady {
			continue
		}
		message := strings.TrimSpace(condition.Message)
		switch condition.Status {
		case corev1.ConditionTrue:
			return "ready", message, schedulable, nil
		case corev1.ConditionFalse:
			return "not-ready", message, schedulable, nil
		default:
			return "unknown", message, schedulable, nil
		}
	}
	return "unknown", "Kubernetes Node has no Ready condition", schedulable, nil
}

func (c *Controller) Ready(ctx context.Context) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	if _, err := c.nodes.List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		return fmt.Errorf("check Kubernetes API readiness: %w", err)
	}
	return nil
}

func (c *Controller) CordonNode(ctx context.Context, nodeName string) error {
	return c.setNodeUnschedulable(ctx, nodeName, true)
}

func (c *Controller) DrainNode(ctx context.Context, nodeName string) error {
	if err := c.CordonNode(ctx, nodeName); err != nil {
		return err
	}
	if c.pods == nil || c.evictions == nil {
		return fmt.Errorf("Kubernetes pod eviction client is not configured")
	}
	pods, err := c.pods.List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + nodeName})
	if err != nil {
		return fmt.Errorf("list Pods on Kubernetes Node %q: %w", nodeName, err)
	}
	for _, pod := range pods.Items {
		stateMode := pod.Labels["deployer.io/state-mode"]
		resilienceMode := pod.Labels["deployer.io/resilience-mode"]
		if stateMode == "" || stateMode == "stateful" || resilienceMode == appconfig.ResiliencePinned {
			continue
		}
		err := c.evictions.Evict(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
		})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("evict Pod %q from Kubernetes Node %q: %w", pod.Name, nodeName, err)
		}
	}
	return nil
}

func (c *Controller) UncordonNode(ctx context.Context, nodeName string) error {
	return c.setNodeUnschedulable(ctx, nodeName, false)
}

func (c *Controller) setNodeUnschedulable(ctx context.Context, nodeName string, unschedulable bool) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	node, err := c.nodes.Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Kubernetes Node %q: %w", nodeName, err)
	}
	if node.Spec.Unschedulable == unschedulable {
		return nil
	}
	node.Spec.Unschedulable = unschedulable
	if _, err := c.nodes.Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Kubernetes Node %q: %w", nodeName, err)
	}
	return nil
}

func (c *Controller) RemoveNode(ctx context.Context, nodeName string) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	err := c.nodes.Delete(ctx, nodeName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Kubernetes Node %q: %w", nodeName, err)
	}
	return nil
}

func (c *Controller) RuntimeStatus(ctx context.Context, appName string) (string, int32, int32, []string, error) {
	state, desired, available, err := c.StatusDetails(ctx, appName)
	if err != nil || c.pods == nil {
		return state, desired, available, nil, err
	}
	pods, err := c.pods.List(ctx, metav1.ListOptions{LabelSelector: "deployer.io/app=" + appName})
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("list Pods for app %q status: %w", appName, err)
	}
	nodeSet := map[string]struct{}{}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning || strings.TrimSpace(pod.Spec.NodeName) == "" {
			continue
		}
		nodeSet[pod.Spec.NodeName] = struct{}{}
	}
	runningNodes := make([]string, 0, len(nodeSet))
	for nodeName := range nodeSet {
		runningNodes = append(runningNodes, nodeName)
	}
	sort.Strings(runningNodes)
	return state, desired, available, runningNodes, nil
}
