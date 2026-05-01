YARA engine (native)
=====================

This package provides a pluggable YARA engine used by the MCP host. By
default the code uses a lightweight mock implementation so the repository
builds on machines without `libyara` installed. To enable the native
libyara-backed engine, follow the platform-specific steps below and build
with the `yara` build tag.

macOS (Homebrew)
-----------------

1. Install `yara` and `pkg-config`:

```bash
brew install yara pkg-config
```

2. Build the project with the `yara` tag:

```bash
CGO_ENABLED=1 go build -tags=yara ./cmd/orchestrator
```

Ubuntu / Debian
---------------

1. Install libyara development headers:

```bash
sudo apt-get update
sudo apt-get install -y libyara-dev pkg-config
```

2. Build with the `yara` tag:

```bash
CGO_ENABLED=1 go build -tags=yara ./cmd/orchestrator
```

Notes
-----
- When building with `-tags=yara`, the file `real_yara.go` is compiled and
  the `Engine` implementation will call into `libyara` via the `go-yara`
  bindings.
- Ensure `pkg-config` can find the `yara` library (Homebrew and apt packages
  usually provide the necessary `.pc` files).
- If you encounter linker errors, verify `libyara` is installed and visible
  to the compiler toolchain.
