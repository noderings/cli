# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
