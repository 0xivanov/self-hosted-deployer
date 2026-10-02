# Self-Hosted Deployer

Self-Hosted Deployer is a CLI-first platform for deploying containerized applications across trusted Linux servers, including Raspberry Pis and small VPS instances.

The platform is designed around a stable VPS control plane, WireGuard private networking, k3s workers, and resilient stateless workloads.

## Documentation

- [Product Spec](PRODUCT_SPEC.md)
- [Implementation Plan](IMPLEMENTATION_PLAN.md)
- [VPS + Raspberry Pi end-to-end setup](docs/vps-raspberry-pi-e2e.md)
- [PostgreSQL high availability](docs/postgres-ha.md)
- [Retained local application storage](docs/retained-local-storage.md)

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

Operational and release builds require a clean Git checkout or dedicated
worktree with no extra ignored source files. Commit the reviewed source first,
then optionally set `EXPECTED_COMMIT` to the complete object ID as an
assertion. Build metadata is derived from Git and cannot be replaced with a
caller-provided `COMMIT` value. Ordinary `make build` remains available during
development, but a dirty build reports `<HEAD>-dirty` and cannot satisfy an
exact release identity check.

- [Operations guide](docs/operations.md)
- [PostgreSQL high availability](docs/postgres-ha.md)
- [VPS + Raspberry Pi end-to-end setup](docs/vps-raspberry-pi-e2e.md)
- Systemd unit templates: `deploy/systemd/`
- Environment examples: `deploy/env/`
- Agent installer: `scripts/install-agent.sh`
- CloudNativePG installer: `scripts/install-cnpg.sh`
- Prometheus, Alertmanager, Loki, Alloy, and Grafana installer: `scripts/install-monitoring.sh`
- MVP smoke test: `scripts/smoke-test.sh`

## Protobuf

Protobuf source files live in `proto/deployer/v1`. Generated Go code is written to `internal/proto/deployer/v1`.

```bash
make proto
make proto-lint
make proto-check
```
