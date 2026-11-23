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

## Generated API client

The client in `internal/api/generated/` contains only the backend operations
the CLI calls. They are listed in `scripts/openapi-allowlist.txt`. To call a
new operation, add its operationId there and regenerate from the backend
Swagger file:

```bash
make generate-api-client SWAGGER_SPEC=/path/to/proto.swagger.json
```

`scripts/openapi-allowlist.py` drops every other operation, the schemas only
they use, and all descriptions. `make leak-check` (also run in CI) fails if the
client contains an operation that is not on the allow-list or not called by
the CLI, or admin, billing, or internal infrastructure details. Maintainers can
add private patterns with `OPENAPI_LEAK_EXTRA_PATTERNS_FILE=/path/to/file`.

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
