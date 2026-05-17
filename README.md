# Self-Hosted Deployer

Self-Hosted Deployer is a CLI-first platform for deploying containerized applications across trusted Linux servers, including Raspberry Pis and small VPS instances.

The platform is designed around a stable VPS control plane, WireGuard private networking, k3s workers, and resilient stateless workloads.

## Documentation

- [Product Spec](PRODUCT_SPEC.md)
- [Implementation Plan](IMPLEMENTATION_PLAN.md)

## Local Development

```bash
make fmt
make test
make build
```

Run the binaries during development:

```bash
go run ./cmd/deployer --help
go run ./cmd/deployer-server --help
go run ./cmd/deployer-agent --help
```

## Protobuf

Protobuf source files live in `proto/deployer/v1`. Generated Go code is written to `internal/proto/deployer/v1`.

```bash
make proto
make proto-lint
make proto-check
```
