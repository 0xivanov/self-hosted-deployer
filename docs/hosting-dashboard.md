# Hosting Overview dashboard

`deploy/monitoring/grafana/dashboards/hosting-overview.json` is a generic Grafana dashboard for the single Kubernetes environment installed by this repository. It uses the provisioned Prometheus data source and has namespace and deployment variables. The default namespace is `deployer-apps`; the deployment variable is populated from the current kube-state-metrics deployment series.

The dashboard is deliberately limited to metrics that the current monitoring stack can scrape:

- desired and available deployment replicas from `kube_deployment_spec_replicas` and `kube_deployment_status_replicas_available`;
- pod container restart counters from `kube_pod_container_status_restarts_total`;
- configured per-container CPU and memory requests and limits from kube-state-metrics;
- node Ready state from `kube_node_status_condition`.

The CPU and memory panels show Kubernetes configuration, not live consumption or saturation. The stack does not scrape kubelet or cAdvisor, so `container_cpu_usage_seconds_total` and working-set metrics are not available. The application selector affects replica panels only. Restart and resource panels explicitly show the entire namespace, and node readiness covers the entire cluster. Resource rows are scoped to the selected namespace and are shown by pod and container because the current kube-state-metrics invocation does not provide a guaranteed deployment-to-pod ownership label in the scraped series.

This dashboard does not claim ingress availability, certificate issuance or expiry, namespace quota usage, backup status, alert delivery, multi-cluster state, or monetary usage. The current Prometheus configuration has no Traefik, cert-manager, ResourceQuota, backup, billing, or second-cluster scrape targets. Although Prometheus has the static external label `cluster: self-hosted-deployer`, the dashboard intentionally does not expose a cluster selector or imply that multiple clusters are configured.

The existing installer loads every file in this directory into the `grafana-dashboards` ConfigMap with `kubectl create configmap ... --from-file="$GRAFANA_DASHBOARDS"`, then mounts that ConfigMap at `/etc/grafana/dashboards`. The existing file dashboard provider watches that directory every 30 seconds, so running `scripts/install-monitoring.sh` provisions this dashboard alongside the existing dashboards. No installer code change is required. Installing it on a running environment is a monitoring configuration change; this work has not run that installer on the live fleet.

Run `sh tests/hosting-dashboard-test.sh` to parse the dashboard and validate its panel expressions with Prometheus promtool. This does not prove live scrape data or a rendered Grafana layout; those remain deployment checks.
