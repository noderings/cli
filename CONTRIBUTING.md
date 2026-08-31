# Contributing

Thanks for your interest in the NodeRings CLI (`nr`).

## Development setup

```bash
git clone https://github.com/noderings/cli.git
cd cli
go mod download
make build
make test
```

Requirements:

- Go version matching `go.mod`
- Make
- Optional: [golangci-lint](https://golangci-lint.run/), `govulncheck`

## Before opening a pull request

```bash
make fmt
make vet
make lint
make test
make vuln
```

Do not edit generated files under `internal/api/generated/` by hand.

## Code style

- Go, with packages under `internal/`
- Prefer small, focused commits and PRs
- Handle errors; do not ignore with `_`
- Do not commit secrets, tokens, kubeconfigs, or hypervisor instances files

## Pull requests

- Describe the problem and approach
- Ensure CI passes (tests, lint, govulncheck)
- Update the README or `docs/` when user-facing behavior changes

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
