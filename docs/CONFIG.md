# Configuration

Global config lives at `~/.nr/config.yaml`. You do not need a config file for a normal production install.

## API

```yaml
api:
  base_url: "https://api.noderings.com"
  timeout: 20
  tls_insecure: false
  ca_cert_path: ""
```

API URL precedence:

1. `--api-url`
2. `NR_API_URL`
3. `api.base_url` (default `https://api.noderings.com`)

Do not set `tls_insecure: true` against production.

## Authentication

```yaml
auth:
  token: ""
  token_file: "~/.nr/tokens"
  refresh_threshold: 300
```

Token resolution order:

1. `NR_API_TOKEN` (also accepts `NODERINGS_API_TOKEN`, `NR_TOKEN`, `NODERINGS_TOKEN`)
2. `auth.token` in config
3. OS keyring / `auth.token_file` (OAuth)

File fallback stores tokens as JSON with mode `0600` (not encrypted). Prefer the OS keyring for interactive use.

```bash
nr auth set-token --token "..."
# or
export NR_API_TOKEN="..."
nr auth set-token --from-env
```

## Logging

```yaml
logging:
  level: "info"
  file: "~/.nr/nr.log"
```

## Hypervisor credentials

`nr cluster register` installs the matching operator after peering. Credentials stay on **your** agent cluster as Kubernetes Secrets. NodeRings does not store them.

### Proxmox (`--hypervisor-driver proxmox`)

Environment (single instance): `PROXMOX_URL`, `PROXMOX_USERNAME`, `PROXMOX_TOKEN_ID`, `PROXMOX_TOKEN_SECRET`.

TLS verification is off. No flag or env var is required.

Or `--proxmox-instances-file` (file mode must be `600`):

```yaml
instances:
  - id: pve-ams-1
    url: https://pve.example.com:8006
    username: nrings-operator@pve
    tokenId: nrings-operator-token
    tokenSecret: "..."
```

### VirtFusion (`--hypervisor-driver virtfusion`)

`VIRTFUSION_URL`, `VIRTFUSION_TOKEN`, `VIRTFUSION_USER_API_TOKEN`, `VIRTFUSION_USER_ID`, `VIRTFUSION_USER_NAME`, or `--virtfusion-instances-file`.

YAML instances (mode `0600`):

```yaml
instances:
  - id: vf-1
    url: https://cp.example.com
    token: "…"            # Global API token (Settings → Global API)
    userApiToken: "…"     # User API token (log in as the client user → Account → API)
    userId: 5             # numeric id of that client user
    userName: noderings   # panel username of that client user
```

Create a **normal VirtFusion client user** (not an admin). Copy the numeric **user id**. Log in as that user and generate a User API token. VMs are created as that user so the operator can `POST /api/server/{uuid}/build` with `userData` (chosen password and client cloud-init).

TLS verification is off. No flag or env var is required.

### SolusVM 2 (`--hypervisor-driver solusvm`)

`SOLUSVM_URL`, `SOLUSVM_TOKEN`, or `--solusvm-instances-file`. Use the SolusVM 2 management-node HTTPS URL (port 443), not SolusVM 1 `:5656`.

TLS verification is off. No flag or env var is required.

## State

Registration checkpoints live under `~/.nr/`. Do not copy these directories between machines.

Never commit tokens, kubeconfigs, or instances files to version control. If a token may have leaked, rotate it and re-run `nr cluster register --resume --reinstall-operator`.
