# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
