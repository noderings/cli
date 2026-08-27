package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/noderings/cli/internal/config"
)

func runLiqoctlJSON(ctx context.Context, kubeconfig string, args ...string) (string, error) {
	displayArgs := append([]string(nil), args...)
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
		displayArgs = append(displayArgs, "--kubeconfig", "<redacted>")
	}
	cmd := exec.CommandContext(ctx, config.LiqoctlBinary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return "", fmt.Errorf("liqoctl %s: %w", strings.Join(displayArgs, " "), err)
		}
		return "", fmt.Errorf("liqoctl %s: %s", safeLiqoctlVerb(displayArgs), msg)
	}
	return stdout.String(), nil
}

func safeLiqoctlVerb(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func liqoInfoReady(output string) bool {
	var infoData map[string]any
	if err := json.Unmarshal([]byte(output), &infoData); err != nil {
		return false
	}
	local, _ := infoData["local"].(map[string]any)
	clusterID, _ := local["clusterID"].(string)
	if strings.TrimSpace(clusterID) == "" {
		return false
	}
	if health, ok := infoData["health"].(map[string]any); ok {
		healthy, ok := health["healthy"].(bool)
		return ok && healthy
	}
	return true
}

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

func peeringComplete(output string) bool {
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

func parseInfoPeerEntries(output string) []peeringInfoPeer {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	// Object keyed by peer name (liqoctl info peer -o json).
	var asMap map[string]peeringInfoPeer
	if err := json.Unmarshal([]byte(output), &asMap); err == nil && len(asMap) > 0 {
		out := make([]peeringInfoPeer, 0, len(asMap))
		for _, p := range asMap {
			out = append(out, p)
		}
		return out
	}

	var asSlice []peeringInfoPeer
	if err := json.Unmarshal([]byte(output), &asSlice); err == nil {
		return asSlice
	}

	var single peeringInfoPeer
	if err := json.Unmarshal([]byte(output), &single); err == nil {
		return []peeringInfoPeer{single}
	}
	return nil
}
