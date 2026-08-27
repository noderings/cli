# NodeRings CLI (`nr`)

The **`nr`** command-line tool is how NodeRings providers connect a dedicated Ubuntu VM as an agent cluster. It authenticates to NodeRings, registers the agent, and installs the local stack used to run customer virtual machines.

## Requirements

- Linux (Ubuntu 22.04+ recommended) for `nr cluster register`
- `curl`, `tar`, and `sudo`
- Outbound HTTPS to GitHub Releases and `api.noderings.com`
- A NodeRings provider organization that is approved for marketplace listing

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/noderings/cli/main/scripts/install.sh | bash
nr version
```

The script installs the latest linux or macOS release (`amd64` or `arm64`) into `/usr/local/bin`. To install somewhere else:

```bash
curl -fsSL https://raw.githubusercontent.com/noderings/cli/main/scripts/install.sh | NR_INSTALL_DIR="$HOME/.local/bin" bash
```

See [Installation](docs/INSTALL.md) for checksums, source builds, and `go install`.

## Sign in

On a VM without a browser:

```bash
nr auth login --no-browser
```

Complete the printed URL on another device. If the VM has a browser, use `nr auth login`.

You can sign in before marketplace approval. Creating agents and `nr cluster register` stay blocked until the organization is approved; those commands print `Provider organization review is pending.`

For automation, create a service account token in **Access Control → Service Accounts**, then:

```bash
export NR_API_TOKEN="..."
nr auth status
```

## Register an agent

Run this on the Ubuntu VM that will host the agent:

```bash
nr cluster register \
  --name edge-ams-01 \
  --agent-ip 203.0.113.10 \
  --gateway-region AMS01 \
  --hypervisor-driver proxmox \
  --organization-id <provider-org-uuid>
```

Supported `--hypervisor-driver` values: `proxmox` (default), `virtfusion`, `solusvm`.

`--organization-id` is the provider organization UUID. The UI copies it into the command. OAuth login otherwise uses your default (often client) org, and listing agents is denied. You can also `export NR_ORGANIZATION_ID=...`.

The CLI prompts for hypervisor credentials unless you pass an instances file:

| Driver | Flag |
|--------|------|
| Proxmox | `--proxmox-instances-file` |
| VirtFusion | `--virtfusion-instances-file` |
| SolusVM 2 | `--solusvm-instances-file` |

One organization should use one hypervisor driver.

If registration is interrupted:

```bash
nr cluster register --resume --name edge-ams-01
```

## Common commands

```bash
nr auth status
nr cluster status --name edge-ams-01
nr cluster verify --name edge-ams-01
nr cluster health --name edge-ams-01
nr cluster deregister --name edge-ams-01
```

Diagnostic messages go to **stderr**. Use `--output json` when you need machine-readable **stdout**. Exit codes: `0` success, `1` runtime failure, `2` usage error.

## Configuration

Optional config lives at `~/.nr/config.yaml`. Production API default is `https://api.noderings.com`. See [Configuration](docs/CONFIG.md).

## Docs

- [Install](docs/INSTALL.md)
- [Configuration](docs/CONFIG.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Security](SECURITY.md)
- [Contributing](CONTRIBUTING.md)
- [Changelog](CHANGELOG.md)

## License

Licensed under the [Apache License 2.0](LICENSE).
