package k8s

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// WaitForRollout waits for a deployment rollout to complete
func (c *Client) WaitForRollout(ctx context.Context, namespace, deployment string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			dep, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
			if err != nil {
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for deployment %s/%s: %w", namespace, deployment, err)
				}
				continue
			}

			// Check if rollout is complete
			if dep.Status.ReadyReplicas == *dep.Spec.Replicas && dep.Status.ReadyReplicas == dep.Status.UpdatedReplicas {
				// Check rollout conditions
				for _, condition := range dep.Status.Conditions {
					if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionTrue {
						if condition.Reason == "NewReplicaSetAvailable" {
							return nil
						}
					}
				}
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for deployment %s/%s rollout (ready: %d/%d, updated: %d/%d)",
					namespace, deployment,
					dep.Status.ReadyReplicas, *dep.Spec.Replicas,
					dep.Status.UpdatedReplicas, *dep.Spec.Replicas)
			}
		}
	}
}

// WaitForPod waits for a pod to be ready
func (c *Client) WaitForPod(ctx context.Context, namespace, podName string, timeout time.Duration) error {
	watcher, err := c.clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.SingleObject(metav1.ObjectMeta{
		Name:      podName,
		Namespace: namespace,
	}))
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-watcher.ResultChan():
			if event.Type == watch.Error {
				return fmt.Errorf("watch error: %v", event.Object)
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			// Check if pod is ready
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					return nil
				}
			}

			// Check if pod failed
			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("pod %s/%s failed", namespace, podName)
			}

		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for pod %s/%s to be ready", namespace, podName)
			}
		}
	}
}

// WaitForNodes waits for nodes to be ready
func (c *Client) WaitForNodes(ctx context.Context, count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for nodes: %w", err)
				}
				continue
			}

			readyCount := 0
			for _, node := range nodes.Items {
				for _, condition := range node.Status.Conditions {
					if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
						readyCount++
						break
					}
				}
			}

			if readyCount >= count {
				return nil
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for %d ready nodes (found %d)", count, readyCount)
			}
		}
	}
}
