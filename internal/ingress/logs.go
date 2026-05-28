package ingress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultLogTailLines int32 = 100

func (c *Controller) StreamLogs(ctx context.Context, appName string, tailLines int32, follow bool, send func(string) error) error {
	if c.pods == nil {
		return fmt.Errorf("Kubernetes pod client is not configured")
	}
	appName = strings.TrimSpace(appName)
	pods, err := c.pods.List(ctx, metav1.ListOptions{LabelSelector: "deployer.io/app=" + appName})
	if err != nil {
		return fmt.Errorf("list Pods for app %q logs: %w", appName, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no running Pods found for app %q", appName)
	}
	sort.Slice(pods.Items, func(i int, j int) bool {
		return pods.Items[i].Name < pods.Items[j].Name
	})
	if tailLines == 0 {
		tailLines = defaultLogTailLines
	}
	prefixPod := len(pods.Items) > 1
	if !follow {
		for _, pod := range pods.Items {
			if err := c.streamPodLogs(ctx, pod.Name, tailLines, false, prefixPod, send); err != nil {
				return err
			}
		}
		return nil
	}
	return c.followPodLogs(ctx, pods.Items, tailLines, prefixPod, send)
}

type podLogMessage struct {
	line string
	err  error
}

func (c *Controller) followPodLogs(ctx context.Context, pods []corev1.Pod, tailLines int32, prefixPod bool, send func(string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	messages := make(chan podLogMessage)
	var wg sync.WaitGroup
	for _, pod := range pods {
		podName := pod.Name
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.streamPodLogs(ctx, podName, tailLines, true, prefixPod, func(line string) error {
				select {
				case messages <- podLogMessage{line: line}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				select {
				case messages <- podLogMessage{err: err}:
				case <-ctx.Done():
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(messages)
	}()

	var firstErr error
	for message := range messages {
		if message.err != nil {
			if firstErr == nil {
				firstErr = message.err
				cancel()
			}
			continue
		}
		if firstErr == nil {
			if err := send(message.line); err != nil {
				firstErr = err
				cancel()
			}
		}
	}
	return firstErr
}

func (c *Controller) streamPodLogs(ctx context.Context, podName string, tailLines int32, follow bool, prefixPod bool, send func(string) error) error {
	tail := int64(tailLines)
	reader, err := c.pods.GetLogs(podName, &corev1.PodLogOptions{
		Follow:    follow,
		TailLines: &tail,
	}).Stream(ctx)
	if err != nil {
		return fmt.Errorf("open logs for Pod %q: %w", podName, err)
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if prefixPod {
			line = podName + ": " + line
		}
		if err := send(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read logs for Pod %q: %w", podName, err)
	}
	return nil
}
