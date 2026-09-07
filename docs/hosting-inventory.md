# Existing environment inventory

Run `scripts/hosting-inventory.sh` locally on each of the three Linux nodes once SSH connection instructions are available. It only reads versions, unit state, capacity, and selected Kubernetes fields. It never reads environment files, Kubernetes Secrets, logs, database contents, or private keys. It does not stop timers or update services.

The output still includes private infrastructure names and addresses. Keep it in an operator-controlled location outside the repository. Review it before sharing.

Record separately, without copying secret values:

- Which node is the ingress, WireGuard hub, k3s server, and workload worker.
- Exact installed release commits and available rollback artifacts/checksums.
- Database/storage locations, encryption-key custody, backup timestamp, and last successful restore.
- Each application's external health URL, representative functional check, expected replicas, and acceptable recovery targets.
- Whether any update service is currently running, each timer's prior state, and the chosen maintenance window.

Do not publish a new stable release until the update-freeze procedure in [the POC plan](hosting-poc-plan.md) has been executed and checked on every node. The current deployed updater cannot honor policy features that have not yet been installed.

Inventory is not a restore test or production qualification. Missing Kubernetes access and uninspected roles must be resolved before a rollout.
