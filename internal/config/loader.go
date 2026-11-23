package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Loader loads configuration from various sources
type Loader struct {
	viper *viper.Viper
}

// NewLoader creates a new config loader
func NewLoader() *Loader {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	// GetConfigDir resolves the real home directory; viper does not expand "$HOME".
	v.AddConfigPath(GetConfigDir())
	v.AddConfigPath(".")

	return &Loader{
		viper: v,
	}
}

// Load loads configuration
func (l *Loader) Load() (*Config, error) {
	// Set defaults
	l.setDefaults()

	// Read config file (optional)
	if err := l.viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	// Read environment variables
	l.viper.AutomaticEnv()

	var config Config
	if err := l.viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	applyMothershipProfile(&config)

	return &config, nil
}

// setDefaults sets default configuration values
func (l *Loader) setDefaults() {
	l.viper.SetDefault("api.base_url", DefaultAPIBaseURL)
	l.viper.SetDefault("api.timeout", 20)
	l.viper.SetDefault("api.tls_insecure", false)
	l.viper.SetDefault("frontend.base_url", DefaultFrontendBaseURL)

	// Local control-plane profile: set mothership.host to derive api + frontend URLs.
	l.viper.SetDefault("mothership.api_port", DefaultMothershipAPIPort)
	l.viper.SetDefault("mothership.frontend_port", DefaultMothershipFrontendPort)
	l.viper.SetDefault("mothership.k8s_api_port", DefaultMothershipK8sAPIPort)
	l.viper.SetDefault("mothership.tls_insecure", true)
	l.viper.SetDefault("logging.level", "info")
	l.viper.SetDefault("downloads.verify_checksums", true)
	l.viper.SetDefault("downloads.keep_on_error", false)

	// k3s defaults
	l.viper.SetDefault("k3s.install_script_url", "https://get.k3s.io")
	l.viper.SetDefault("k3s.kubeconfig_mode", "600")
	l.viper.SetDefault("k3s.cluster_cidr", "10.200.0.0/16")
	l.viper.SetDefault("k3s.service_cidr", "10.201.0.0/16")
	l.viper.SetDefault("k3s.flannel_backend", "none")
	l.viper.SetDefault("k3s.install_disables", "--disable-network-policy --disable=traefik")
	l.viper.SetDefault("k3s.install_channel", "")
	l.viper.SetDefault("k3s.version", "")
	l.viper.SetDefault("k3s.min_version", "v1.25.0")

	// Calico defaults
	l.viper.SetDefault("calico.version", "v3.30.2")
	l.viper.SetDefault("calico.rollout_timeout", "10m")
	l.viper.SetDefault("calico.min_version", "v3.26.0")

	// Liqo defaults
	l.viper.SetDefault("liqo.version", DefaultLiqoVersion)
	l.viper.SetDefault("liqo.timeout", "10m")
	l.viper.SetDefault("liqo.gw_service_type", DefaultGWServiceType)
	l.viper.SetDefault("liqo.gw_server_service_location", DefaultGWServerServiceLocation)
	l.viper.SetDefault("liqo.pod_offloading_strategy", DefaultPodOffloadingStrategy)
	l.viper.SetDefault("liqo.pod_cidr", "10.200.0.0/16")
	l.viper.SetDefault("liqo.service_cidr", "10.201.0.0/16")
	l.viper.SetDefault("liqo.min_version", DefaultLiqoVersion)
	l.viper.SetDefault("liqo.chart_oci", DefaultLiqoChartOCI)
	l.viper.SetDefault("liqo.chart_version", DefaultLiqoChartVersion)
	l.viper.SetDefault("liqo.proxy_url", "")
	l.viper.SetDefault("liqo.api_server_url", "")
	l.viper.SetDefault("liqo.gw_server_service_nodeport", "")
	l.viper.SetDefault("liqo.gw_client_address", "")
	l.viper.SetDefault("liqo.gw_client_port", "")
	l.viper.SetDefault("liqo.inbound_api_proxy", DefaultInboundAPIProxy)

	// Common env var names (AutomaticEnv would expect API_BASE_URL for api.base_url;
	// these match docs and local dev usage.)
	_ = l.viper.BindEnv("api.base_url", "NR_API_URL", "API_BASE_URL", "NODERINGS_API_URL")
	_ = l.viper.BindEnv("api.tls_insecure", "NR_API_TLS_INSECURE", "API_TLS_INSECURE")
	_ = l.viper.BindEnv("frontend.base_url", "NR_FRONTEND_URL", "NODERINGS_FRONTEND_URL", "FRONTEND_BASE_URL")

	_ = l.viper.BindEnv("mothership.host", "NR_MOTHERSHIP_HOST", "NODERINGS_MOTHERSHIP_HOST")
	_ = l.viper.BindEnv("mothership.api_port", "NR_MOTHERSHIP_API_PORT")
	_ = l.viper.BindEnv("mothership.frontend_port", "NR_MOTHERSHIP_FRONTEND_PORT")
	_ = l.viper.BindEnv("mothership.k8s_api_port", "NR_MOTHERSHIP_K8S_API_PORT")
	_ = l.viper.BindEnv("mothership.tls_insecure", "NR_MOTHERSHIP_TLS_INSECURE")
	_ = l.viper.BindEnv("liqo.gw_client_address", "NR_LIQO_GW_CLIENT_ADDRESS")
	_ = l.viper.BindEnv("liqo.gw_client_port", "NR_LIQO_GW_CLIENT_PORT")
}

// applyMothershipProfile fills api.base_url, frontend.base_url, and optionally api.tls_insecure
// when mothership.host is set (YAML or NR_MOTHERSHIP_HOST). Use this for a single LAN IP / DNS
// name for a local control-plane host (gateway + UI). Explicit NR_API_URL / NR_FRONTEND_URL /
// NR_API_TLS_INSECURE still override the derived values.
func applyMothershipProfile(c *Config) {
	host := strings.TrimSpace(c.Mothership.Host)
	if host == "" {
		return
	}

	apiPort := c.Mothership.APIPort
	if apiPort == 0 {
		apiPort = DefaultMothershipAPIPort
	}
	fePort := c.Mothership.FrontendPort
	if fePort == 0 {
		fePort = DefaultMothershipFrontendPort
	}

	if !apiURLOverriddenByEnv() {
		c.API.BaseURL = fmt.Sprintf("https://%s:%d", host, apiPort)
	}
	if !frontendURLOverriddenByEnv() {
		c.Frontend.BaseURL = fmt.Sprintf("http://%s:%d", host, fePort)
	}
	if !apiTLSInsecureOverriddenByEnv() {
		c.API.TLSInsecure = c.Mothership.TLSInsecure
	}
}

func apiURLOverriddenByEnv() bool {
	for _, k := range []string{"NR_API_URL", "API_BASE_URL", "NODERINGS_API_URL"} {
		if _, set := os.LookupEnv(k); set {
			return true
		}
	}
	return false
}

func frontendURLOverriddenByEnv() bool {
	for _, k := range []string{"NR_FRONTEND_URL", "NODERINGS_FRONTEND_URL", "FRONTEND_BASE_URL"} {
		if _, set := os.LookupEnv(k); set {
			return true
		}
	}
	return false
}

func apiTLSInsecureOverriddenByEnv() bool {
	for _, k := range []string{"NR_API_TLS_INSECURE", "API_TLS_INSECURE"} {
		if _, set := os.LookupEnv(k); set {
			return true
		}
	}
	return false
}

// Config represents the application configuration
type Config struct {
	API        APIConfig        `mapstructure:"api"`
	Frontend   FrontendConfig   `mapstructure:"frontend"`
	Mothership MothershipConfig `mapstructure:"mothership"`
	Auth       AuthConfig       `mapstructure:"auth"`
	Logging    LoggingConfig    `mapstructure:"logging"`
	Downloads  DownloadsConfig  `mapstructure:"downloads"`
	Clusters   ClustersConfig   `mapstructure:"clusters"`
	K3s        K3sConfig        `mapstructure:"k3s"`
	Calico     CalicoConfig     `mapstructure:"calico"`
	Liqo       LiqoConfig       `mapstructure:"liqo"`
}

// APIConfig holds API configuration
type APIConfig struct {
	BaseURL     string `mapstructure:"base_url"`
	Timeout     int    `mapstructure:"timeout"`
	TLSInsecure bool   `mapstructure:"tls_insecure"`
	CACertPath  string `mapstructure:"ca_cert_path"`
}

// FrontendConfig holds frontend configuration
type FrontendConfig struct {
	BaseURL string `mapstructure:"base_url"`
}

// MothershipConfig points nr at a local control-plane host. When host is
// non-empty, api.base_url and frontend.base_url are derived unless overridden by env vars.
type MothershipConfig struct {
	Host         string `mapstructure:"host"`
	APIPort      int    `mapstructure:"api_port"`
	FrontendPort int    `mapstructure:"frontend_port"`
	// K8sAPIHost is the control-plane Kubernetes API address used in peering kubeconfig
	// rewrites. When empty, nr probes mothership.host (and other candidates) against
	// the peering CA and picks the first TLS-valid host. Set this when the HTTP gateway
	// host differs from the Kubernetes API certificate SAN.
	K8sAPIHost  string `mapstructure:"k8s_api_host"`
	K8sAPIPort  int    `mapstructure:"k8s_api_port"`
	TLSInsecure bool   `mapstructure:"tls_insecure"`
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Token            string `mapstructure:"token"`             // Service account token (JWT) - set via env var or config file
	TokenFile        string `mapstructure:"token_file"`        // OAuth token storage path - used by 'nr auth login'
	RefreshThreshold int    `mapstructure:"refresh_threshold"` // Seconds before expiry to refresh OAuth token
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level string `mapstructure:"level"`
	File  string `mapstructure:"file"`
}

// DownloadsConfig holds download configuration
type DownloadsConfig struct {
	TempDir         string `mapstructure:"temp_dir"`
	KeepOnError     bool   `mapstructure:"keep_on_error"`
	VerifyChecksums bool   `mapstructure:"verify_checksums"`
}

// ClustersConfig holds clusters configuration
type ClustersConfig struct {
	DefaultClusterID string `mapstructure:"default_cluster_id"`
}

// K3sConfig holds k3s installation configuration
type K3sConfig struct {
	InstallScriptURL string `mapstructure:"install_script_url"`
	KubeconfigMode   string `mapstructure:"kubeconfig_mode"`
	ClusterCIDR      string `mapstructure:"cluster_cidr"`
	ServiceCIDR      string `mapstructure:"service_cidr"`
	FlannelBackend   string `mapstructure:"flannel_backend"`
	InstallDisables  string `mapstructure:"install_disables"`
	InstallChannel   string `mapstructure:"install_channel"`
	Version          string `mapstructure:"version"`
	MinVersion       string `mapstructure:"min_version"`
}

// CalicoConfig holds Calico installation configuration
type CalicoConfig struct {
	Version        string `mapstructure:"version"`
	RolloutTimeout string `mapstructure:"rollout_timeout"`
	MinVersion     string `mapstructure:"min_version"`
}

// LiqoConfig holds Liqo installation configuration
type LiqoConfig struct {
	Version                 string `mapstructure:"version"`
	Timeout                 string `mapstructure:"timeout"`
	GWServiceType           string `mapstructure:"gw_service_type"`
	GWServerServiceLocation string `mapstructure:"gw_server_service_location"`
	PodOffloadingStrategy   string `mapstructure:"pod_offloading_strategy"`
	PodCIDR                 string `mapstructure:"pod_cidr"`
	ServiceCIDR             string `mapstructure:"service_cidr"`
	MinVersion              string `mapstructure:"min_version"`
	ProxyURL                string `mapstructure:"proxy_url"`
	APIServerURL            string `mapstructure:"api_server_url"`
	GWServerServiceNodePort string `mapstructure:"gw_server_service_nodeport"`
	GWClientAddress         string `mapstructure:"gw_client_address"`
	GWClientPort            string `mapstructure:"gw_client_port"`
	// InboundAPIProxy controls whether the control plane reaches this cluster's API server
	// through the Liqo api-server-proxy when peering back: auto, always, or never.
	InboundAPIProxy string `mapstructure:"inbound_api_proxy"`
	// ChartOCI is the Helm OCI chart reference without version (e.g. oci://harbor.noderings.com/nrings/liqo).
	ChartOCI string `mapstructure:"chart_oci"`
	// ChartVersion is the Helm chart version (usually Version without a leading v).
	ChartVersion string `mapstructure:"chart_version"`
}

// GetConfigDir returns the config directory path
func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.nr"
	}
	return filepath.Join(home, ".nr")
}
