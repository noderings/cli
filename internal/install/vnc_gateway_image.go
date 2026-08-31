package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noderings/cli/internal/config"
)

// vncGatewayImageRef returns repository:tag, defaulting the Harbor repo.
func vncGatewayImageRef(repository, tag string) string {
	repo := strings.TrimSpace(repository)
	if repo == "" {
		repo = config.DefaultVNCGatewayImageRepository
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = config.DefaultVNCGatewayImageTagRFB
	}
	return repo + ":" + tag
}

// usesLocalRFBImage reports whether this tag should be loaded from a local-rfb
// build instead of pulling Harbor stock :main.
func usesLocalRFBImage(tag string) bool {
	tag = strings.TrimSpace(tag)
	return tag != "" && !strings.EqualFold(tag, config.DefaultVNCGatewayImageTagProxmox)
}

func findVNCGatewaySource() string {
	if env := strings.TrimSpace(os.Getenv("VNC_GATEWAY_SOURCE")); env != "" {
		if st, err := os.Stat(filepath.Join(env, "Dockerfile")); err == nil && !st.IsDir() {
			return env
		}
	}
	for _, candidate := range []string{
		filepath.Join("..", "vnc-gateway"),
		filepath.Join("..", "..", "vnc-gateway"),
	} {
		if st, err := os.Stat(filepath.Join(candidate, "Dockerfile")); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func dockerImageExists(ctx context.Context, image string) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// EnsureRFBVNCGatewayImage builds vnc-gateway:local-rfb from the sibling checkout
// (if needed) and imports it into k3s as repository:tag. Proxmox agents must not
// call this — they pull Harbor :main. No-ops when SKIP_VNC_GATEWAY_IMAGE_LOAD=1
// or when the tag is Harbor stock main.
func EnsureRFBVNCGatewayImage(ctx context.Context, repository, tag string, logger Logger) error {
	if envBool(config.EnvSkipVNCGatewayImageLoad) {
		logger.Info("Skipping local-rfb vnc-gateway load (SKIP_VNC_GATEWAY_IMAGE_LOAD)")
		return nil
	}
	if !usesLocalRFBImage(tag) {
		logger.Info("Skipping local-rfb vnc-gateway load (tag is Harbor stock main)")
		return nil
	}
	ref := vncGatewayImageRef(repository, tag)
	if containerdHasImage(ctx, ref) {
		logger.Infof("containerd already has %s", ref)
		return nil
	}
	if err := ensureLocalRFBDockerImage(ctx, ref, logger); err != nil {
		logger.Warnf("Could not build local-rfb vnc-gateway %s: %v (Helm will try to pull it)", ref, err)
		return nil
	}
	if err := importDockerImageToContainerd(ctx, ref, logger); err != nil {
		return fmt.Errorf("import %s into containerd: %w (or pull harbor.noderings.com/noderings/vnc-gateway:rfb)", ref, err)
	}
	logger.Infof("Loaded local-rfb vnc-gateway as %s", ref)
	return nil
}

func ensureLocalRFBDockerImage(ctx context.Context, ref string, logger Logger) error {
	if dockerImageExists(ctx, ref) {
		logger.Infof("Using existing docker image %s", ref)
		return nil
	}
	local := config.LocalRFBVNCGatewayImage
	if dockerImageExists(ctx, local) {
		logger.Infof("Tagging %s -> %s", local, ref)
		return runDocker(ctx, logger, "tag", local, ref)
	}
	src := findVNCGatewaySource()
	if src == "" {
		return fmt.Errorf("no %s image and no vnc-gateway source (set VNC_GATEWAY_SOURCE or place a sibling checkout)", local)
	}
	logger.Infof("Building local-rfb vnc-gateway from %s", src)
	if err := runDocker(ctx, logger, "build", "--platform", "linux/amd64", "-f", "Dockerfile", "-t", local, "-t", ref, src); err != nil {
		return err
	}
	return nil
}

func runDocker(ctx context.Context, logger Logger, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logger.Info(strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func importDockerImageToContainerd(ctx context.Context, image string, logger Logger) error {
	importer, err := pickContainerdImporter()
	if err != nil {
		return err
	}

	save := exec.CommandContext(ctx, "docker", "save", image)
	stdout, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("docker save pipe: %w", err)
	}
	save.Stderr = os.Stderr
	if err := save.Start(); err != nil {
		return fmt.Errorf("docker save %s: %w", image, err)
	}

	cmd := containerdCmd(ctx, importer...)
	cmd.Stdin = stdout
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		_ = save.Wait()
		return fmt.Errorf("%s: %w", strings.Join(importer, " "), runErr)
	}
	if waitErr := save.Wait(); waitErr != nil {
		return fmt.Errorf("docker save %s: %w", image, waitErr)
	}
	logger.Infof("Imported %s via %s", image, strings.Join(importer, " "))
	return nil
}

func pickContainerdImporter() ([]string, error) {
	if _, err := exec.LookPath("k3s"); err == nil {
		return []string{"k3s", "ctr", "images", "import", "-"}, nil
	}
	if _, err := exec.LookPath("ctr"); err == nil {
		return []string{"ctr", "-n", "k8s.io", "images", "import", "-"}, nil
	}
	// Provider agents often have k3s only on root's PATH.
	if _, err := exec.LookPath("sudo"); err == nil {
		return []string{"k3s", "ctr", "images", "import", "-"}, nil
	}
	return nil, fmt.Errorf("no k3s/ctr importer found")
}

func containerdHasImage(ctx context.Context, image string) bool {
	importer, err := pickContainerdImporter()
	if err != nil {
		return false
	}
	if len(importer) < 2 {
		return false
	}
	ls := append([]string{}, importer[:len(importer)-2]...)
	ls = append(ls, "ls", "-q")
	cmd := containerdCmd(ctx, ls...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), image)
}

func containerdCmd(ctx context.Context, args ...string) *exec.Cmd {
	if len(args) == 0 {
		return exec.CommandContext(ctx, "false")
	}
	if os.Geteuid() == 0 {
		return exec.CommandContext(ctx, args[0], args[1:]...)
	}
	sudoArgs := append([]string{"-n"}, args...)
	return exec.CommandContext(ctx, "sudo", sudoArgs...)
}
