# Troubleshooting

## Authentication

**`nr auth status` shows unauthenticated**

- Run `nr auth login` (or `nr auth login --no-browser` on a headless VM).
- Or set `NR_API_TOKEN` from a service account token.
- Confirm you can reach `https://api.noderings.com`.

**`Provider organization review is pending.`**

Login succeeded; the provider organization is not marketplace-approved yet. Identity and organization commands still work. Creating agents, generating install commands, and peering stay blocked until review completes. A verified organization can run the same provider APIs.

**TLS / certificate errors**

- Prefer a proper CA with `api.ca_cert_path`.
- `api.tls_insecure: true` and `--dev` skip verification — not for production.

## Install

**Installer cannot resolve the latest release**

- Confirm [github.com/noderings/cli](https://github.com/noderings/cli) is reachable and has a published release.
- Download the archive from [Releases](https://github.com/noderings/cli/releases) and install it manually (see [Installation](INSTALL.md)).

## Cluster register

**Prechecks fail (ports, disk, memory, OS)**

- Use a dedicated Ubuntu 22.04+ VM with free ports including `6443`.
- Use `--skip-prechecks` only when you understand the risk.

**`--agent-ip` is not assigned to any local interface**

`--agent-ip` is passed to k3s as `--node-ip`. It must exist on this VM (`ip -4 addr`), not a docs placeholder (`1.1.1.1`) and not a NAT address that is only visible upstream. Use the address on `eth0` / `ens*` (for example `192.168.1.153`).

**`You are not allowed to perform this action.` on register**

OAuth sessions bind to the user's home organization, which may be a client org. Pass the provider org UUID:

```bash
nr cluster register --organization-id <provider-org-uuid> ...
```

The UI **Install with NR CLI** command includes this flag. Also accepted: `NR_ORGANIZATION_ID`.

**Resume after failure**

```bash
nr cluster register --resume --name <same-name>
```

Checkpoints live under `~/.nr/`. Use `nr cluster status` and `nr cluster debug` for details.

**Post-register verification failed**

```bash
nr cluster verify --name <name>
nr cluster verify --name <name> --output=json
```

Failed verify/health exits `1`. Bad flags exit `2`.

## Deregister

```bash
nr cluster deregister --name <name>
```

`--force` continues after partial failures. Review leftovers on the VM if a force teardown was used.

## Diagnostics

```bash
nr cluster status
nr cluster info
nr cluster health
nr cluster logs
nr cluster debug
```

`debug` writes a report under `~/.nr/debug-<timestamp>/`. Redact tokens before sharing.

## Getting help

- Security reports: [SECURITY.md](../SECURITY.md)
- Product docs: [docs.noderings.com](https://docs.noderings.com/docs/cli)
