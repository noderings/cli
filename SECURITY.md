# Security Policy

## Supported Versions

| Version | Supported |
| ------- | --------- |
| main    | Yes       |
| 0.x     | Yes       |

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Report security issues privately:

1. Open [https://github.com/noderings/cli/security/advisories/new](https://github.com/noderings/cli/security/advisories/new), or
2. Use [GitHub private vulnerability reporting](https://github.com/noderings/cli/security/advisories) if enabled for this repository.

Include:

- A description of the issue and potential impact
- Steps to reproduce
- Affected versions or components (`nr` CLI, install tooling, auth/token storage)

We aim to acknowledge reports within a few business days and will coordinate disclosure and fixes with you.

## Security notes for operators

- Prefer OS keyring token storage; file fallback writes tokens as JSON with mode `0600` (not encrypted).
- Do not use `--dev` or `api.tls_insecure: true` against production APIs.
- Rotate service account tokens regularly and never commit them to version control.
