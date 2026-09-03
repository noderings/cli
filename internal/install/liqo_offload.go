package install

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// namespaceOffloadingGVR is the Liqo CR that binds a local namespace to a remote name.
// Re-register with a new agent ID cannot change RemoteNamespaceName in place.
var namespaceOffloadingGVR = schema.GroupVersionResource{
	Group:    "offloading.liqo.io",
	Version:  "v1beta1",
	Resource: "namespaceoffloadings",
}

// remoteNamespaceNameFromUnstructured reads spec.remoteNamespaceName, then status.
func remoteNamespaceNameFromUnstructured(obj map[string]any) string {
	if spec, _ := obj["spec"].(map[string]any); spec != nil {
		if name, ok := spec["remoteNamespaceName"].(string); ok && name != "" {
			return name
		}
	}
	if status, _ := obj["status"].(map[string]any); status != nil {
		if name, ok := status["remoteNamespaceName"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

// offloadNeedsRemap is true when an existing NamespaceOffloading targets a different
// remote name than this register wants. Liqo rejects in-place RemoteNamespaceName edits.
func offloadNeedsRemap(currentRemote, wantedRemote, localNS string) bool {
	if currentRemote == "" {
		return false
	}
	if wantedRemote == "" {
		wantedRemote = localNS
	}
	return currentRemote != wantedRemote
}

// recreateOffloadIfRemoteNameChanged drops a leftover offload from a previous agent ID
// so liqoctl offload can create one with the new remote name. alreadyOK is true when
// the existing CR already uses the wanted remote name (caller should skip liqoctl offload).
func (l *LiqoManager) recreateOffloadIfRemoteNameChanged(ctx context.Context, ns, remoteNamespaceName string) (alreadyOK bool, err error) {
	current, found, err := l.currentOffloadRemoteName(ctx, ns)
	if err != nil {
		l.logger.Warnf("Could not inspect existing offload for %s: %v", ns, err)
		return false, nil
	}
	if !found {
		return false, nil
	}
	if !offloadNeedsRemap(current, remoteNamespaceName, ns) {
		l.logger.Infof("Namespace %s already offloaded as %q", ns, current)
		return true, nil
	}

	wanted := remoteNamespaceName
	if wanted == "" {
		wanted = ns
	}
	l.logger.Infof("Remote namespace for %s changed (%s → %s); unoffloading first", ns, current, wanted)
	if err := l.unoffloadNamespace(ctx, ns); err != nil {
		return false, fmt.Errorf("unoffload %s before remapping remote name: %w", ns, err)
	}
	return false, nil
}

func (l *LiqoManager) currentOffloadRemoteName(ctx context.Context, ns string) (string, bool, error) {
	dyn, err := l.dynamicClient()
	if err != nil {
		return "", false, err
	}
	list, err := dyn.Resource(namespaceOffloadingGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) || isMissingAPI(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if len(list.Items) == 0 {
		return "", false, nil
	}
	return remoteNamespaceNameFromUnstructured(list.Items[0].Object), true, nil
}

func (l *LiqoManager) unoffloadNamespace(ctx context.Context, ns string) error {
	args := []string{"unoffload", "namespace", ns, "--skip-confirm"}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	if err := l.runLiqoctl(ctx, args...); err != nil {
		l.logger.Warnf("liqoctl unoffload %s: %v (will delete leftover NamespaceOffloading)", ns, err)
	}
	return l.deleteNamespaceOffloading(ctx, ns)
}

func (l *LiqoManager) deleteNamespaceOffloading(ctx context.Context, ns string) error {
	dyn, err := l.dynamicClient()
	if err != nil {
		return err
	}
	return deleteNamespaceOffloadingsIn(ctx, dyn, l.logger, ns)
}

func (l *LiqoManager) dynamicClient() (dynamic.Interface, error) {
	if l.k8sClient == nil || l.k8sClient.GetConfig() == nil {
		return nil, fmt.Errorf("kubernetes client is not configured")
	}
	dyn, err := dynamic.NewForConfig(l.k8sClient.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}
	return dyn, nil
}

func isMissingAPI(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "the server could not find")
}
