package install

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed liqo-provider-values.yaml
var liqoProviderValuesYAML []byte

// writeProviderValuesFile writes the embedded provider Liqo Helm values to a temp file.
// Caller must remove the returned path when done.
func writeProviderValuesFile() (string, error) {
	f, err := os.CreateTemp("", "nr-liqo-provider-values-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create liqo values temp file: %w", err)
	}
	path := f.Name()
	if _, err := f.Write(liqoProviderValuesYAML); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write liqo values: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
