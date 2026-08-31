package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/k8s"
)

// finalizerGraceTimeout is how long a graceful delete may run before finalizers are cleared.
const finalizerGraceTimeout = 30 * time.Second

// Bounds for waiting on the platform to drop its half of the peering after DeleteAgent.
const (
	platformUnpeerTimeout      = 3 * time.Minute
	platformUnpeerPollInterval = 3 * time.Second
)

// ClusterCleaner removes local cluster components during deregister/force.
type ClusterCleaner struct {
	logger Logger
	sudo   *SudoManager
}

// NewClusterCleaner creates a cluster cleaner.
func NewClusterCleaner(logger Logger) (*ClusterCleaner, error) {
	if _, err := EnsureCLIBinDir(); err != nil {
		return nil, fmt.Errorf("prepare CLI bin dir: %w", err)
	}
	sudo, err := NewSudoManager(logger)
	if err != nil {
		return nil, err
	}
	return &ClusterCleaner{logger: logger, sudo: sudo}, nil
}

// WaitForPlatformUnpeer blocks until the platform has removed the ForeignCluster it owns.
//
// DeleteAgent only enqueues that teardown, so continuing immediately races it: while the
// platform still holds the peering its controllers recreate any ForeignCluster deleted here,
// and `liqoctl uninstall` refuses to run while one exists. Waiting turns that race into a
// deterministic handover.
func (c *ClusterCleaner) WaitForPlatformUnpeer(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, platformUnpeerTimeout)
	defer cancel()

	c.logger.Info("Waiting for the platform to drop its peering...")

	var remaining []string
	err := wait.PollUntilContextCancel(waitCtx, platformUnpeerPollInterval, true, func(ctx context.Context) (bool, error) {
		names, listErr := c.listForeignClusterNames(ctx)
		if listErr != nil {
			// Liqo may already be partially torn down here; let local cleanup make the call.
			return false, nil
		}
		remaining = names
		return len(names) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("platform still holds a peering after %s (ForeignCluster(s): %s)",
			platformUnpeerTimeout, strings.Join(remaining, ", "))
	}

	c.logger.Info("✓ Platform peering removed")
	return nil
}

// UnpeerLiqo best-effort unpeers Liqo using liqoctl.
// remoteKubeconfig is optional; local kubeconfig is always resolved to a
// user-readable path (k3s.yaml is often mode 600 / root-owned).
// After liqoctl unpeer, leftover ForeignCluster CRs are deleted — uninstall
// pre-checks fail while authentication remains enabled on those objects.
func (c *ClusterCleaner) UnpeerLiqo(ctx context.Context, remoteKubeconfig string) error {
	if _, err := exec.LookPath("liqoctl"); err != nil {
		return fmt.Errorf("liqoctl not found: %w", err)
	}

	localKubeconfig := EnsureReadableKubeconfig(ctx, "", c.logger)
	args := []string{"unpeer", "--skip-confirm"}
	if localKubeconfig != "" {
		args = append(args, "--kubeconfig", localKubeconfig)
	}
	if remoteKubeconfig != "" {
		args = append(args, "--remote-kubeconfig", remoteKubeconfig)
	}
	c.logger.Info("Unpeering Liqo clusters...")
	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	unpeerErr := cmd.Run()
	if unpeerErr != nil {
		c.logger.Warnf("liqoctl unpeer returned an error (will still clean ForeignClusters): %v", unpeerErr)
	}

	if err := c.deleteForeignClusters(ctx); err != nil {
		if unpeerErr != nil {
			return fmt.Errorf("liqoctl unpeer: %w; also failed to delete ForeignClusters: %v", unpeerErr, err)
		}
		return fmt.Errorf("delete leftover ForeignClusters: %w", err)
	}

	remaining, listErr := c.listForeignClusterNames(ctx)
	if listErr != nil {
		c.logger.Warnf("Could not verify ForeignCluster cleanup: %v", listErr)
		if unpeerErr != nil {
			return fmt.Errorf("liqoctl unpeer: %w", unpeerErr)
		}
		return nil
	}
	if len(remaining) > 0 {
		return fmt.Errorf("ForeignClusters still present after cleanup: %s (disable authentication / delete these before uninstall)", strings.Join(remaining, ", "))
	}
	if unpeerErr != nil {
		// Unpeer may fail when peering was already partially torn down; empty FC list is enough.
		c.logger.Info("No ForeignClusters remain; treating unpeer as complete")
	}
	return nil
}

// UnoffloadNamespaces disables Liqo namespace offloading so uninstall does not hang.
// liqoctl uninstall can hang silently when NamespaceOffloading resources remain.
func (c *ClusterCleaner) UnoffloadNamespaces(ctx context.Context) error {
	if _, err := exec.LookPath("liqoctl"); err != nil {
		return fmt.Errorf("liqoctl not found: %w", err)
	}

	namespaces, err := c.listOffloadedNamespaces(ctx)
	if err != nil {
		return err
	}
	if len(namespaces) == 0 {
		c.logger.Info("No offloaded namespaces found")
		return nil
	}

	kubeconfigPath := EnsureReadableKubeconfig(ctx, "", c.logger)
	args := append([]string{"unoffload", "namespace"}, namespaces...)
	args = append(args, "--skip-confirm")
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	c.logger.Infof("Unoffloading namespaces: %s", strings.Join(namespaces, ", "))
	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("liqoctl unoffload: %w", err)
	}
	return nil
}

// UninstallLiqo best-effort uninstalls Liqo via liqoctl.
func (c *ClusterCleaner) UninstallLiqo(ctx context.Context, kubeconfigPath string) error {
	if _, err := exec.LookPath("liqoctl"); err != nil {
		return fmt.Errorf("liqoctl not found: %w", err)
	}

	kubeconfigPath = EnsureReadableKubeconfig(ctx, kubeconfigPath, c.logger)
	args := []string{"uninstall", "--purge", "--skip-confirm"}
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	c.logger.Info("Uninstalling Liqo (may take several minutes)...")
	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		names, listErr := c.listForeignClusterNames(ctx)
		if listErr != nil {
			c.logger.Warnf("Could not list ForeignClusters after uninstall failure: %v", listErr)
		} else if len(names) > 0 {
			// A ForeignCluster that survives local cleanup means the platform still holds a
			// peering towards this cluster and its controllers keep recreating the object.
			return fmt.Errorf(
				"liqoctl uninstall: %w\nForeignCluster(s) still present: %s\n"+
					"The platform still holds (or just held) a peering towards this cluster.\n"+
					"  - Agent still on the platform: delete it there (or re-run deregister without --skip-api),\n"+
					"    wait for the platform to unpeer, then finish locally with --skip-api\n"+
					"  - Agent already deleted: retry with --skip-api",
				err, strings.Join(names, ", "))
		}
		return fmt.Errorf("liqoctl uninstall: %w", err)
	}
	return nil
}

// UninstallCalico best-effort removes Calico namespaces/resources.
func (c *ClusterCleaner) UninstallCalico(ctx context.Context) error {
	client, err := newReadableK8sClient(ctx, c.logger)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	c.logger.Info("Removing Calico resources (best-effort)...")
	cs := client.GetClientset()
	namespaces := []string{config.DefaultCalicoSystemNamespace, config.DefaultCalicoAPIServerNamespace, config.DefaultTigeraOperatorNamespace}
	for _, ns := range namespaces {
		err := cs.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		if err != nil && !strings.Contains(err.Error(), "not found") {
			c.logger.Warnf("Failed to delete namespace %s: %v", ns, err)
		}
	}
	return nil
}

// UninstallK3s runs the k3s uninstall script if present.
func (c *ClusterCleaner) UninstallK3s(ctx context.Context) error {
	candidates := []string{
		"/usr/local/bin/k3s-uninstall.sh",
		"/usr/bin/k3s-uninstall.sh",
	}
	script := ""
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			script = path
			break
		}
	}
	if script == "" {
		return fmt.Errorf("k3s uninstall script not found")
	}

	c.logger.Infof("Uninstalling k3s via %s...", script)
	cmd := c.sudo.RunCommand(ctx, "sh", script)
	cmd.Stdin = c.sudo.PrepareStdin(nil)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("k3s uninstall: %w", err)
	}
	return nil
}

// ForceCleanupAll runs best-effort cleanup of Liqo, Calico, and k3s.
func (c *ClusterCleaner) ForceCleanupAll(ctx context.Context, peeringKubeconfig string) []error {
	var errs []error

	if err := c.UnpeerLiqo(ctx, peeringKubeconfig); err != nil {
		c.logger.Warnf("Unpeer Liqo: %v", err)
		errs = append(errs, err)
	}
	if err := c.UnoffloadNamespaces(ctx); err != nil {
		c.logger.Warnf("Unoffload namespaces: %v", err)
		errs = append(errs, err)
	}
	// liqoctl uninstall refuses while leftover tenant networking CRs remain.
	if err := c.CleanupLiqoTenantLeftovers(ctx); err != nil {
		c.logger.Warnf("Cleanup Liqo tenant leftovers: %v", err)
		errs = append(errs, err)
	}
	if err := c.UninstallLiqo(ctx, ""); err != nil {
		c.logger.Warnf("Uninstall Liqo: %v", err)
		errs = append(errs, err)
	}
	if err := c.UninstallCalico(ctx); err != nil {
		c.logger.Warnf("Uninstall Calico: %v", err)
		errs = append(errs, err)
	}
	if err := c.UninstallK3s(ctx); err != nil {
		c.logger.Warnf("Uninstall k3s: %v", err)
		errs = append(errs, err)
	}
	return errs
}

// DetectInstalledComponents probes the host/cluster for installed pieces.
func DetectInstalledComponents(ctx context.Context) (k3s, calico, liqo, apiReachable bool) {
	if _, err := exec.LookPath("k3s"); err == nil {
		k3s = true
	}
	if _, err := os.Stat("/etc/rancher/k3s/k3s.yaml"); err == nil {
		k3s = true
	}
	if cmd := exec.Command("systemctl", "is-active", "--quiet", "k3s"); cmd.Run() == nil {
		k3s = true
	}

	client, err := newReadableK8sClient(ctx, nil)
	if err != nil {
		return k3s, false, false, false
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.WaitForAPI(timeoutCtx); err == nil {
		apiReachable = true
	} else {
		return k3s, false, false, false
	}

	cs := client.GetClientset()
	if _, err := cs.CoreV1().Namespaces().Get(ctx, config.DefaultCalicoSystemNamespace, metav1.GetOptions{}); err == nil {
		calico = true
	}
	if _, err := cs.CoreV1().Namespaces().Get(ctx, config.DefaultTigeraOperatorNamespace, metav1.GetOptions{}); err == nil {
		calico = true
	}
	if _, err := cs.CoreV1().Namespaces().Get(ctx, config.DefaultLiqoNamespace, metav1.GetOptions{}); err == nil {
		liqo = true
	}
	return k3s, calico, liqo, apiReachable
}

func newReadableK8sClient(ctx context.Context, logger Logger) (*k8s.Client, error) {
	path := EnsureReadableKubeconfig(ctx, "", logger)
	if path == "" {
		return nil, fmt.Errorf("could not find a readable kubeconfig")
	}
	return k8s.NewClient(path)
}

func (c *ClusterCleaner) listOffloadedNamespaces(ctx context.Context) ([]string, error) {
	client, err := newReadableK8sClient(ctx, c.logger)
	if err != nil {
		return nil, fmt.Errorf("create k8s client: %w", err)
	}

	dyn, err := dynamic.NewForConfig(client.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "offloading.liqo.io",
		Version:  "v1beta1",
		Resource: "namespaceoffloadings",
	}
	list, err := dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Liqo may already be gone or CRD not installed.
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil, nil
		}
		return nil, fmt.Errorf("list namespaceoffloadings: %w", err)
	}

	seen := map[string]struct{}{}
	var namespaces []string
	for _, item := range list.Items {
		ns := item.GetNamespace()
		if ns == "" {
			continue
		}
		if _, ok := seen[ns]; ok {
			continue
		}
		seen[ns] = struct{}{}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

func (c *ClusterCleaner) listForeignClusterNames(ctx context.Context) ([]string, error) {
	dyn, err := c.dynamicClient(ctx)
	if err != nil {
		return nil, err
	}
	list, err := dyn.Resource(foreignClusterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil, nil
		}
		return nil, fmt.Errorf("list foreignclusters: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

func (c *ClusterCleaner) deleteForeignClusters(ctx context.Context) error {
	dyn, err := c.dynamicClient(ctx)
	if err != nil {
		return err
	}

	list, err := dyn.Resource(foreignClusterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return fmt.Errorf("list foreignclusters: %w", err)
	}
	if len(list.Items) == 0 {
		return nil
	}

	c.logger.Infof("Deleting %d leftover ForeignCluster(s)...", len(list.Items))
	for _, item := range list.Items {
		name := item.GetName()
		c.logger.Infof("Deleting ForeignCluster %q", name)
		if err := dyn.Resource(foreignClusterGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			if strings.Contains(err.Error(), "not found") {
				continue
			}
			return fmt.Errorf("delete foreigncluster %s: %w", name, err)
		}
		// Liqo finalizers revoke credentials and tenant state on the remote peer, so give
		// them a chance to run; only force removal when deletion is genuinely stuck.
		if err := c.forceDeleteIfStuck(ctx, dyn.Resource(foreignClusterGVR), name); err != nil {
			return fmt.Errorf("delete foreigncluster %s: %w", name, err)
		}
	}
	return nil
}

// forceDeleteIfStuck waits for a deleting object to disappear and, if a finalizer blocks it,
// clears the finalizers so local cleanup can proceed.
func (c *ClusterCleaner) forceDeleteIfStuck(ctx context.Context, client dynamic.ResourceInterface, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, finalizerGraceTimeout)
	defer cancel()

	err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		if _, err := client.Get(ctx, name, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, nil
		}
		return false, nil
	})
	if err == nil {
		return nil
	}

	obj, getErr := client.Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return nil
		}
		return getErr
	}
	if len(obj.GetFinalizers()) == 0 {
		return nil
	}

	c.logger.Warnf("Finalizers still block deletion of %s; clearing them", name)
	obj.SetFinalizers(nil)
	if _, err := client.Update(ctx, obj, metav1.UpdateOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("clear finalizers on %s: %w", name, err)
	}
	return nil
}

func (c *ClusterCleaner) dynamicClient(ctx context.Context) (dynamic.Interface, error) {
	client, err := newReadableK8sClient(ctx, c.logger)
	if err != nil {
		return nil, fmt.Errorf("create k8s client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(client.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}
	return dyn, nil
}

// leftoverTenantGVRs are networking/IPAM objects liqoctl uninstall refuses to remove
// while they remain under liqo-tenant-* namespaces after a partial unpeer.
var leftoverTenantGVRs = []schema.GroupVersionResource{
	{Group: "networking.liqo.io", Version: "v1beta1", Resource: "configurations"},
	{Group: "ipam.liqo.io", Version: "v1alpha1", Resource: "ips"},
	{Group: "ipam.liqo.io", Version: "v1alpha1", Resource: "networks"},
}

// CleanupLiqoTenantLeftovers deletes leftover tenant CRs (and namespaces) that block
// `liqoctl uninstall` after ForeignClusters were removed but networking objects remain.
func (c *ClusterCleaner) CleanupLiqoTenantLeftovers(ctx context.Context) error {
	client, err := newReadableK8sClient(ctx, c.logger)
	if err != nil {
		// Cluster already gone (e.g. mid k3s wipe) — nothing to clean.
		c.logger.Infof("Skipping Liqo tenant leftover cleanup: %v", err)
		return nil
	}
	dyn, err := dynamic.NewForConfig(client.GetConfig())
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	nsList, err := client.GetClientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var tenantNS []string
	for _, ns := range nsList.Items {
		if strings.HasPrefix(ns.Name, "liqo-tenant-") {
			tenantNS = append(tenantNS, ns.Name)
		}
	}
	if len(tenantNS) == 0 {
		return nil
	}

	c.logger.Infof("Cleaning Liqo tenant leftovers in %d namespace(s)...", len(tenantNS))
	for _, ns := range tenantNS {
		for _, gvr := range leftoverTenantGVRs {
			if err := c.deleteNamespacedGVR(ctx, dyn, gvr, ns); err != nil {
				c.logger.Warnf("Failed cleaning %s in %s: %v", gvr.Resource, ns, err)
			}
		}
		c.logger.Infof("Deleting namespace %q", ns)
		if err := client.GetClientset().CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			if !strings.Contains(err.Error(), "not found") {
				c.logger.Warnf("Failed to delete namespace %s: %v", ns, err)
			}
		}
	}
	return nil
}

func (c *ClusterCleaner) deleteNamespacedGVR(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string) error {
	list, err := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return err
	}
	for _, item := range list.Items {
		name := item.GetName()
		c.logger.Infof("Deleting %s %s/%s", gvr.Resource, namespace, name)
		if err := dyn.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			if strings.Contains(err.Error(), "not found") {
				continue
			}
			return fmt.Errorf("delete %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}
		if err := c.forceDeleteIfStuck(ctx, dyn.Resource(gvr).Namespace(namespace), name); err != nil {
			return fmt.Errorf("delete %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}
	}
	return nil
}
