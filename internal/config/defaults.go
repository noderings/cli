package config

// DefaultConfig returns default configuration values
func DefaultConfig() *Config {
	return &Config{
		API: APIConfig{
			BaseURL:     DefaultAPIBaseURL,
			Timeout:     20,
			TLSInsecure: false,
		},
		//nolint:gosec // G101: TokenFile is a filesystem path, not a credential
		Auth: AuthConfig{
			TokenFile:        "~/.nr/tokens",
			RefreshThreshold: 300,
		},
		Logging: LoggingConfig{
			Level: "info",
			File:  "~/.nr/nr.log",
		},
		Downloads: DownloadsConfig{
			TempDir:         "",
			KeepOnError:     false,
			VerifyChecksums: true,
		},
		Clusters: ClustersConfig{
			DefaultClusterID: "",
		},
	}
}
