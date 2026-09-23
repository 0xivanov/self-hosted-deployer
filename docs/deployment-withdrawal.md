# Confirmed withdrawal after a failed apply

Status: source implementation only. Not installed on the live VPS or workers. This is bounded recovery for explicitly confirmed failures, not a complete durable deployment-operation protocol.

`DeployAppRequest.report_withdrawal` opts into structured confirmation. The equivalent CLI is `deployer deploy --report-withdrawal --file app.yaml`. Existing clients retain ordinary failure responses.

For a definitive Kubernetes API rejection during apply, or a route/configuration commit failure after apply returned successfully, the server attempts compensation. An existing app restores its previous configuration, environment revision, registry credential revision, runtime resources and routes. A failed initial deployment removes its partial runtime resources/routes and marks its app deleted. Encrypted environment and registry revisions remain staged so a retry can recreate the required Kubernetes Secrets.

The server returns `withdrawal_confirmed: true` only after compensation and failed-deployment persistence both succeed. The response contains the failed deployment identity and the canonical requested configuration, without secret values. Runtime transport errors, timeouts, ambiguous server errors, compensation failures and persistence failures return no proof. Unknown runtime errors are intentionally conservative because an original Kubernetes write may still arrive after a lost reply. Human CLI output reports the failure; JSON mode returns the structured receipt for the fleet worker.

AppService serializes deploy/delete mutations per app within the serving process. Waiting is context-aware; unrelated apps can proceed, and unused lock entries are removed. This is not a cross-process lock or durable execution fence.

The fleet must verify the receipt against the complete successful preflight configuration and intended release, then persist it before making the portal attempt retryable. For an update it must additionally observe the matching failed deployment and fresh runtime/configuration/HTTPS health for the prior release. A generic error or elapsed timeout is not withdrawal proof.

## Remaining recovery work

Lost responses still cannot be recovered authoritatively: there is no durable client operation ID or withdrawal tombstone in core yet. Requests accepted by Kubernetes but stuck later during rollout also remain pending. The next step is a persistent per-app operation protocol with identity, predecessor snapshot, execution/withdrawal states and a runtime fence that prevents stale submissions from activating. Do not enable broad Docker hosting based on this bounded failure path alone.

Local service checks cover successful previous-release compensation, initial cleanup, compensation/persistence failure, timeout refusal and legacy behavior. Fleet checks cover receipt identity, exact preflight matching, prior-release health, restart/no-replay behavior and uncertain responses. No live deployment was changed.
