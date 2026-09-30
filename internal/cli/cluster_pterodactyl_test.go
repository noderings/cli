package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func clearPterodactylEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PTERODACTYL_PANEL_URL",
		"PTERODACTYL_APPLICATION_KEY",
		"PTERODACTYL_APPLICATION_KEY_FILE",
		"PTERODACTYL_CLIENT_KEY",
		"PTERODACTYL_CLIENT_KEY_FILE",
		"PTERODACTYL_PANEL_USER_ID",
		"PTERODACTYL_OPERATOR_CHART",
	} {
		t.Setenv(key, "")
	}
}

func newPterodactylCmd(panelURL, appFile, clientFile, userID string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("panel-url", panelURL, "")
	cmd.Flags().String("application-key-file", appFile, "")
	cmd.Flags().String("client-key-file", clientFile, "")
	cmd.Flags().String("panel-user-id", userID, "")
	cmd.Flags().String("operator-chart", "", "")
	cmd.Flags().String("pterodactyl-chart", "", "")
	return cmd
}

func TestPterodactylHelmArgsDisablesCRDSubchart(t *testing.T) {
	args := pterodactylHelmArgs(pterodactylInstall{
		Chart:      "oci://example.invalid/pterodactyl-operator",
		Namespace:  "pterodactyl-system",
		PanelURL:   "https://panel.example.com",
		SecretName: "operator-pterodactyl-operator-panel-credentials",
	}, "/tmp/kubeconfig")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--set crds.enabled=false") {
		t.Fatalf("operator chart must not adopt CRDs installed before peering: %s", joined)
	}
}

func TestInstallPterodactylRejectsMissingURL(t *testing.T) {
	clearPterodactylEnv(t)
	_, err := loadPterodactylInstall(newPterodactylCmd("", "", "", "1"), "operator-chart", true)
	if !errors.Is(err, errPterodactylCredentialsRequired) {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

func TestPreflightPterodactylAllowsPromptLater(t *testing.T) {
	clearPterodactylEnv(t)
	if err := preflightPterodactylInstall(newPterodactylCmd("", "", "", "")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPterodactylReadsEnv(t *testing.T) {
	clearPterodactylEnv(t)
	t.Setenv("PTERODACTYL_PANEL_URL", "http://192.168.1.111/")
	t.Setenv("PTERODACTYL_APPLICATION_KEY", "ptla_env")
	t.Setenv("PTERODACTYL_CLIENT_KEY", "ptlc_env")
	t.Setenv("PTERODACTYL_PANEL_USER_ID", "4")
	got, err := loadPterodactylInstall(newPterodactylCmd("", "", "", ""), "operator-chart", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.PanelURL != "http://192.168.1.111" || got.ApplicationKey != "ptla_env" || got.ClientKey != "ptlc_env" || got.PanelUserID != "4" {
		t.Fatalf("env credentials were not used: %+v", got)
	}
}

func TestHypervisorDriverRejectsPterodactyl(t *testing.T) {
	_, err := parseHypervisorDriver("pterodactyl")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("pterodactyl must not be a hypervisor driver, got %v", err)
	}
}

func TestInstallPterodactylRejectsHelmInjection(t *testing.T) {
	clearPterodactylEnv(t)
	_, err := loadPterodactylInstall(newPterodactylCmd("http://192.168.1.111,cluster.enabled=false", "", "", "1"), "operator-chart", true)
	if err == nil || !strings.Contains(err.Error(), "--panel-url") {
		t.Fatalf("expected panel url error, got %v", err)
	}
}

func TestInstallPterodactylReadsKeyFiles(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	client := filepath.Join(dir, "client")
	if err := os.WriteFile(app, []byte("ptla_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client, []byte("ptlc_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearPterodactylEnv(t)
	t.Setenv("PTERODACTYL_PANEL_URL", "http://evil.example")
	t.Setenv("PTERODACTYL_APPLICATION_KEY", "ptla_ignored")
	got, err := loadPterodactylInstall(newPterodactylCmd("http://192.168.1.111", app, client, "1"), "operator-chart", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.PanelURL != "http://192.168.1.111" || got.ApplicationKey != "ptla_test" || got.ClientKey != "ptlc_test" {
		t.Fatal("flags did not override env")
	}
	if got.Namespace != "pterodactyl-system" {
		t.Fatalf("namespace %s", got.Namespace)
	}
}

func TestInstallPterodactylReadsKeyFileEnv(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	client := filepath.Join(dir, "client")
	if err := os.WriteFile(app, []byte("ptla_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client, []byte("ptlc_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearPterodactylEnv(t)
	t.Setenv("PTERODACTYL_PANEL_URL", "https://panel.example.com")
	t.Setenv("PTERODACTYL_APPLICATION_KEY_FILE", app)
	t.Setenv("PTERODACTYL_APPLICATION_KEY", "ptla_ignored")
	t.Setenv("PTERODACTYL_CLIENT_KEY_FILE", client)
	t.Setenv("PTERODACTYL_PANEL_USER_ID", "3")
	got, err := loadPterodactylInstall(newPterodactylCmd("", "", "", ""), "operator-chart", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApplicationKey != "ptla_file" || got.ClientKey != "ptlc_file" || got.PanelUserID != "3" {
		t.Fatalf("key files from env were not used: %+v", got)
	}
}
