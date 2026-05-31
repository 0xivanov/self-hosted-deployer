# Self-Hosted Deployer

Self-Hosted Deployer is a CLI-first platform for deploying containerized applications across trusted Linux servers, including Raspberry Pis and small VPS instances.

The platform is designed around a stable VPS control plane, WireGuard private networking, k3s workers, and resilient stateless workloads.

## Documentation

- [Product Spec](PRODUCT_SPEC.md)
- [Implementation Plan](IMPLEMENTATION_PLAN.md)
- [VPS + Raspberry Pi end-to-end setup](docs/vps-raspberry-pi-e2e.md)

## Local Development

```bash
make fmt
make test
make build
```

Install the local CLI during development:

```bash
make install-cli
```

Run the binaries during development:

```bash
go run ./cmd/deployer --help
go run ./cmd/deployer-server --help
go run ./cmd/deployer-agent --help
```

## Operations

Phase 10 operational assets live under `deploy/`, `scripts/`, and `docs/`.

```bash
make build-arm64
make release
```

- [Operations guide](docs/operations.md)
- [VPS + Raspberry Pi end-to-end setup](docs/vps-raspberry-pi-e2e.md)
- Systemd unit templates: `deploy/systemd/`
- Environment examples: `deploy/env/`
- Agent installer: `scripts/install-agent.sh`
- MVP smoke test: `scripts/smoke-test.sh`

## Protobuf

Protobuf source files live in `proto/deployer/v1`. Generated Go code is written to `internal/proto/deployer/v1`.

```bash
make proto
make proto-lint
make proto-check
```
