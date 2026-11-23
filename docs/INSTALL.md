# Installation

Install **`nr`** on the machine that will register the agent (usually a dedicated Ubuntu VM).

## Installer (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/noderings/cli/main/scripts/install.sh | bash
nr version
```

The script:

1. Detects OS (`linux` or `darwin`) and architecture (`amd64` or `arm64`)
2. Downloads the latest GitHub Release archive
3. Verifies `checksums.txt` when present
4. Installs `nr` to `/usr/local/bin` (`sudo` if that directory is not writable)

Override the install location with `NR_INSTALL_DIR`.

Manual download is also available from [GitHub Releases](https://github.com/noderings/cli/releases):

```bash
tar -xzf nr_*_linux_amd64.tar.gz
sudo install -m 0755 nr /usr/local/bin/nr
nr version
```

## From source

Requires Go matching [`go.mod`](../go.mod).

```bash
git clone https://github.com/noderings/cli.git
cd cli
make build
sudo install -m 0755 build/nr /usr/local/bin/nr
```

## Using `go install`

```bash
go install github.com/noderings/cli/cmd/nr@latest
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`.

## Confirm

```bash
nr version
nr --help
```

Next: [authenticate](../README.md#sign-in), then register an agent.
