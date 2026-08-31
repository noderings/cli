package install

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"

	"github.com/noderings/cli/internal/config"
	"github.com/noderings/cli/internal/k8s"
)

// defaultNamespaceAbsenceTimeout applies when liqo.timeout is unset/unparseable.
const defaultNamespaceAbsenceTimeout = 3 * time.Minute

type peeringInfoPeer struct {
	Authentication struct {
		Status         string `json:"status"`
		ResourceSlices []struct {
			Accepted bool `json:"accepted"`
		} `json:"resourceSlices"`
	} `json:"authentication"`
	Network struct {
		Status string `json:"status"`
	} `json:"network"`
	Offloading struct {
		VirtualNodes []any `json:"virtualNodes"`
	} `json:"offloading"`
}

// IsPeeringComplete reports whether liqoctl peer finished (resource slice accepted or virtual node present).
func (l *LiqoManager) IsPeeringComplete(ctx context.Context) (bool, error) {
	output, err := l.liqoctlInfoPeerJSON(ctx)
	if err != nil {
		return false, err
	}
	return parsePeeringComplete(output), nil
}

// NeedsPeeringReset reports partial peering that blocks re-peer (e.g. resource slice stuck unaccepted).
func (l *LiqoManager) NeedsPeeringReset(ctx context.Context) (bool, error) {
	output, err := l.liqoctlInfoPeerJSON(ctx)
	if err != nil {
		return false, err
	}
	return parsePeeringNeedsReset(output), nil
}

// ResetPartialPeering tears down incomplete peering state on local and remote clusters.
// Partial runs often leave nonce secrets or tenant namespaces that block the next peer.
func (l *LiqoManager) ResetPartialPeering(ctx context.Context, remoteKubeconfigPath string) {
	l.logger.Info("Cleaning up partial Liqo peering state before retry...")

	args := []string{"unpeer", "--remote-kubeconfig", remoteKubeconfigPath, "--timeout", l.config.Timeout}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	l.runLiqoctlBestEffort(ctx, args...)

	if err := l.deleteStaleForeignClusters(ctx); err != nil {
		l.logger.Infof("ForeignCluster cleanup: %v", err)
	}
	if err := l.deletePeeringTenantNamespaces(ctx, l.k8sClient); err != nil {
		l.logger.Infof("Local tenant namespace cleanup: %v", err)
	}
	if err := l.deletePeeringNonceSecrets(ctx, l.k8sClient); err != nil {
		l.logger.Infof("Local nonce secret cleanup: %v", err)
	}
	// Remote control-plane cleanup requires admin credentials; the peering-user kubeconfig
	// cannot list cluster-scoped namespaces. EnsureTenantNamespace on the platform API
	// recreates liqo-tenant-<agent-id> before the next peering config is fetched.
}

func (l *LiqoManager) runLiqoctlBestEffort(ctx context.Context, args ...string) {
	var sawBenignUnpeer atomic.Bool
	filter := func(line string) bool {
		if !isBenignUnpeerLogLine(line) {
			return false
		}
		sawBenignUnpeer.Store(true)
		return true
	}

	err := l.runLiqoctlWithLineFilter(ctx, args, filter)
	if err == nil {
		return
	}
	if len(args) > 0 && args[0] == "unpeer" && sawBenignUnpeer.Load() {
		l.logger.Info("liqoctl unpeer: no existing peering to remove")
		return
	}
	l.logger.Infof("liqoctl %s (best effort): %v", args[0], err)
}

// isBenignUnpeerLogLine suppresses liqoctl ERRO noise when unpeer finds no ForeignCluster.
func isBenignUnpeerLogLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "bidirectional peering") &&
		(strings.Contains(lower, "notfound") || strings.Contains(lower, "not found"))
}

func (l *LiqoManager) deletePeeringTenantNamespaces(ctx context.Context, client *k8s.Client) error {
	if client == nil {
		return nil
	}

	nsList, err := client.GetClientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var pending []string
	for _, ns := range nsList.Items {
		if !strings.HasPrefix(ns.Name, "liqo-tenant-") {
			continue
		}
		pending = append(pending, ns.Name)
		if ns.DeletionTimestamp != nil {
			l.logger.Infof("Waiting for peering tenant namespace %q to finish terminating", ns.Name)
			continue
		}
		l.logger.Infof("Deleting peering tenant namespace %q", ns.Name)
		err := client.GetClientset().CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete namespace %s: %w", ns.Name, err)
		}
	}

	for _, name := range pending {
		if err := l.waitForNamespaceAbsence(ctx, client, name); err != nil {
			return err
		}
	}
	return nil
}

func (l *LiqoManager) waitForNamespaceAbsence(ctx context.Context, client *k8s.Client, name string) error {
	l.logger.Infof("Waiting for namespace %q to be fully deleted before re-peer", name)
	timeout, parseErr := time.ParseDuration(l.config.Timeout)
	if parseErr != nil {
		timeout = defaultNamespaceAbsenceTimeout
	}
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, getErr := client.GetClientset().CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}
		if getErr != nil {
			return false, getErr
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("wait for namespace %s deletion: %w", name, err)
	}
	l.logger.Infof("Namespace %q deleted", name)
	return nil
}

func (l *LiqoManager) deletePeeringNonceSecrets(ctx context.Context, client *k8s.Client) error {
	if client == nil {
		return nil
	}

	nsList, err := client.GetClientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	for _, ns := range nsList.Items {
		if !strings.HasPrefix(ns.Name, "liqo-tenant-") {
			continue
		}
		for _, secretName := range []string{"liqo-nonce", "liqo-signed-nonce"} {
			err := client.GetClientset().CoreV1().Secrets(ns.Name).Delete(ctx, secretName, metav1.DeleteOptions{})
			if err == nil {
				l.logger.Infof("Deleted secret %s/%s", ns.Name, secretName)
			}
		}
	}
	return nil
}

var foreignClusterGVR = schema.GroupVersionResource{
	Group:    "core.liqo.io",
	Version:  "v1beta1",
	Resource: "foreignclusters",
}

func (l *LiqoManager) deleteStaleForeignClusters(ctx context.Context) error {
	cfg := l.k8sClient.GetConfig()
	if cfg == nil {
		return nil
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	list, err := dynamicClient.Resource(foreignClusterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list foreignclusters: %w", err)
	}

	for _, item := range list.Items {
		name := item.GetName()
		l.logger.Infof("Deleting ForeignCluster %q after peering reset", name)
		if err := dynamicClient.Resource(foreignClusterGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("delete foreigncluster %s: %w", name, err)
		}
	}
	return nil
}

func (l *LiqoManager) liqoctlInfoPeerJSON(ctx context.Context) (string, error) {
	args := []string{"info", "peer", "-o", config.LiqoctlOutputJSON, "-n", config.DefaultLiqoNamespace}
	if kubeconfigPath := l.getKubeconfigPath(); kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorOutput := strings.TrimSpace(stderr.String())
		if errorOutput == "" {
			errorOutput = strings.TrimSpace(stdout.String())
		}
		if errorOutput != "" {
			return "", fmt.Errorf("liqoctl info peer failed: %s", errorOutput)
		}
		return "", fmt.Errorf("liqoctl info peer failed: %w", err)
	}
	return stdout.String(), nil
}

func parsePeeringComplete(output string) bool {
	for _, peer := range parseInfoPeerEntries(output) {
		for _, slice := range peer.Authentication.ResourceSlices {
			if slice.Accepted {
				return true
			}
		}
		if len(peer.Offloading.VirtualNodes) > 0 {
			return true
		}
	}
	return false
}

func parsePeeringNeedsReset(output string) bool {
	for _, peer := range parseInfoPeerEntries(output) {
		networkOK := strings.EqualFold(peer.Network.Status, "Healthy") ||
			strings.EqualFold(peer.Network.Status, "Established")
		authOK := strings.EqualFold(peer.Authentication.Status, "Healthy")
		if !networkOK || !authOK {
			continue
		}
		if len(peer.Authentication.ResourceSlices) == 0 {
			continue
		}
		for _, slice := range peer.Authentication.ResourceSlices {
			if !slice.Accepted {
				return true
			}
		}
	}
	return false
}

func (l *LiqoManager) hasPartialPeeringState(ctx context.Context) bool {
	if output, err := l.liqoctlInfoPeerJSON(ctx); err == nil && len(parseInfoPeerEntries(output)) > 0 {
		return true
	}
	if err := l.hasForeignClusters(ctx); err == nil {
		return true
	}
	if err := l.hasPeeringTenantNamespaces(ctx, l.k8sClient); err == nil {
		return true
	}
	return false
}

func (l *LiqoManager) hasForeignClusters(ctx context.Context) error {
	cfg := l.k8sClient.GetConfig()
	if cfg == nil {
		return fmt.Errorf("no kubeconfig")
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	list, err := dynamicClient.Resource(foreignClusterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(list.Items) > 0 {
		return nil
	}
	return fmt.Errorf("no foreign clusters")
}

func (l *LiqoManager) hasPeeringTenantNamespaces(ctx context.Context, client *k8s.Client) error {
	if client == nil {
		return fmt.Errorf("no client")
	}
	nsList, err := client.GetClientset().CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, ns := range nsList.Items {
		if strings.HasPrefix(ns.Name, "liqo-tenant-") {
			return nil
		}
	}
	return fmt.Errorf("no peering tenant namespaces")
}

func parseInfoPeerEntries(output string) []peeringInfoPeer {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil
	}

	peersRaw, ok := raw["peers"]
	if ok {
		var peers map[string]peeringInfoPeer
		if err := json.Unmarshal(peersRaw, &peers); err == nil {
			out := make([]peeringInfoPeer, 0, len(peers))
			for _, peer := range peers {
				out = append(out, peer)
			}
			return out
		}
	}

	out := make([]peeringInfoPeer, 0, len(raw))
	for key, value := range raw {
		if key == "local" || key == "health" {
			continue
		}
		var peer peeringInfoPeer
		if err := json.Unmarshal(value, &peer); err != nil {
			continue
		}
		out = append(out, peer)
	}
	return out
}

func (l *LiqoManager) runLiqoctl(ctx context.Context, args ...string) error {
	return l.runLiqoctlWithLineFilter(ctx, args, nil)
}

func (l *LiqoManager) runLiqoctlWithLineFilter(ctx context.Context, args []string, skipLine func(string) bool) error {
	l.logger.Infof("Running: liqoctl %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "liqoctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("liqoctl stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("liqoctl stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("liqoctl start: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	stream := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if skipLine != nil && skipLine(line) {
				continue
			}
			l.logger.Info(line)
		}
	}
	go stream(stdout)
	go stream(stderr)

	err = cmd.Wait()

	wg.Wait()

	if err != nil {
		return fmt.Errorf("liqoctl %s failed: %w", args[0], err)
	}
	return nil
}
