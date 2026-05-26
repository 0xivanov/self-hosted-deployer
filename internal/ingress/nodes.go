package ingress

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func (c *Controller) CordonNode(ctx context.Context, nodeName string) error {
	return c.setNodeUnschedulable(ctx, nodeName, true)
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
