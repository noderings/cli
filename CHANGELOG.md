# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.12] - 2026-09-04

### Added

- Route inbound peering through the Liqo API server proxy when the provider API is not publicly reachable (NAT / RFC1918 `--agent-ip`).

### Fixed

- Unoffload `vnc-gateway` before DeleteAgent and wipe leftover mothership ForeignCluster / tenant / operator objects (and stuck Liqo finalizers) on deregister, including `--skip-api`.
- Remap an existing NamespaceOffloading when the remote namespace name changed so a new agent ID can register on the same host.

### Changed

- Pin Liqo to `v0.0.0-3f1654f0`.

## [1.0.11] - 2026-09-02

### Changed

- Require `--org-id` (or `NR_ORGANIZATION_ID`) for `nr cluster register`. The CLI no longer lists organizations to pick a provider tenant. Authenticate with a service-account token (`NR_API_TOKEN`); `--org-id` only sets `X-Organization-ID` and cannot replace a valid token.

## [1.0.10] - 2026-09-02

### Changed

- Pin Liqo install images to public Harbor `nrings` so `nr cluster register` can pull without credentials.

## [1.0.9] - 2026-09-02

### Changed

- Persist the hypervisor driver on the agent during `nr cluster register`.
- Pull Liqo, operators, and VNC gateway from the public Harbor `nrings` project.

## [1.0.8] - 2026-08-31

### Changed

- Echo all CLI token and sudo prompts. Block leftover-k3s re-register.

## [1.0.7] - 2026-08-29

### Changed

- Echo VirtFusion Global API token and User API token while pasting so it is obvious the paste landed.

## [1.0.6] - 2026-08-29

### Changed

- VirtFusion `nr cluster register` collects a client username, numeric user id, and User API token (Account → API) in addition to the Global Admin token. Pin the VirtFusion operator chart to 0.1.4.

## [1.0.5] - 2026-08-28

### Changed

- Drop `--organization-id` / `NR_ORGANIZATION_ID`. Authenticated commands list organizations and use the account's provider organization (a user has one).

## [1.0.4] - 2026-08-28

### Changed

- Skip hypervisor API TLS verification on install for VirtFusion, Proxmox, and SolusVM. No flag or env var is required.

## [1.0.3] - 2026-08-28

### Changed

- Pin operator image registry to harbor.noderings.com

## [1.0.2] - 2026-08-27

### Changed

- Echo the VirtFusion API token while pasting
- Strip trailing `/` from Proxmox, VirtFusion, and SolusVM API URLs
- Issue Mimir write tokens via API instead of prompting
- Fail register if `--agent-ip` is not on a local interface

## [1.0.1] - 2026-08-27

### Added

- `--organization-id` / `NR_ORGANIZATION_ID` sends `X-Organization-ID` so OAuth logins hit the provider tenant, not the default client org. `nr cluster register` lists provider orgs and selects when the flag is omitted.

## [1.0.0]

### Added

- `nr` CLI for provider authentication, agents, service accounts, and cluster registration
- `nr cluster register` with Proxmox, VirtFusion, and SolusVM 2 hypervisor drivers
- Checkpoint/resume, verify, status, health, logs, debug, and deregister
- GitHub Release installer (`scripts/install.sh`) and GoReleaser multi-arch binaries

## [0.1.0] - TBD

Initial public release.
