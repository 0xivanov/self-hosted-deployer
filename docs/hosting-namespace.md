# Hosting namespace budgets

Each customer hosting environment should use its own VM or dedicated cluster. In that environment, choose a new namespace such as `hosting-customer-a`; do not place customer workloads in the existing `deployer-apps`, `kube-system`, or `default` namespaces. The existing defaults remain unchanged.

`DEPLOYER_INGRESS_NAMESPACE` must exactly match the namespace selected for the deployer server's ingress resources. Set it in that environment's server configuration, for example:

```sh
DEPLOYER_INGRESS_NAMESPACE=hosting-customer-a
```

The namespace bootstrap is preview-only by default:

```sh
scripts/create-hosting-namespace.sh \
  --namespace hosting-customer-a \
  --cpu 4 \
  --memory 8Gi \
  --ephemeral-storage 20Gi \
  --pods 40
```

Review the rendered `ResourceQuota` and `LimitRange` before applying. Applying requires an explicit kubeconfig and Kubernetes context and refuses to touch an existing namespace:

```sh
scripts/create-hosting-namespace.sh --apply \
  --kubeconfig /path/to/customer-a.kubeconfig \
  --context customer-a \
  --namespace hosting-customer-a \
  --cpu 4 \
  --memory 8Gi \
  --ephemeral-storage 20Gi \
  --pods 40
```

The script creates the namespace first, then applies requests and limits for CPU, memory, ephemeral storage, and pods, plus a per-container maximum budget. It does not reclassify existing workloads or adopt an existing namespace. Resource values are intentionally bounded and validated before YAML is rendered. Run `tests/hosting-namespace-test.sh` for mock-only validation; it does not contact a cluster.
