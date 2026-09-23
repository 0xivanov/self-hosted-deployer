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


## Recorded request outcomes (implemented locally)

`deployer deploy --request-id <64-lowercase-hex-id> --file app.yaml` opts into a durable request journal and withdrawal reporting. Read the recorded outcome using `deployer --output json apps request <app-name> <request-id>` with the usual connection options. The lookup does not submit or retry a deployment.

Core schema 9 records the immutable candidate configuration, withdrawal option and predecessor before app mutation. At most one pending request is permitted per app. Reusing a request with changed configuration is rejected. Completed requests return their saved response; pending requests block further deployment and deletion. Terminal results are persisted with a bounded context independent of caller cancellation.

An `applied` result records the server outcome, not current site availability. The fleet worker still verifies the exact app/deployment identity, configuration, replicas and HTTPS route. A confirmed withdrawal requires fresh predecessor health when there was a previous release. Missing records, incomplete requests and ambiguous runtime failures remain pending, without resubmission. Recovery of an actually stuck rollout still requires runtime fencing and is not implemented by this journal.

This migration and the related worker changes are not deployed. Upgrade the server and CLI together before enabling the tracked worker path.

## Isolated candidate and activation primitives

Local runtime foundations now render hosted stateless candidates under per-request Deployment names and selectors. Candidate pods do not match the legacy Service. A generation-specific NetworkPolicy is created and ownership/content checked before candidate creation. Preparation never updates an existing candidate. Environment and registry secret references remain versioned. Candidate comparison is intentionally strict; Kubernetes-defaulted fields must be normalized and qualified before real-cluster replay can be enabled.

The stable Service activation primitive captures its UID and resourceVersion. Fencing changes an operation annotation while preserving the current target. Activation changes selector and ports together using that captured resourceVersion. There is no conflict retry or automatic recapture. API conflicts, a recreated Service and changed ownership reject an old token. A focused simulated API race verifies that a delayed old activation cannot overwrite a recovered selector, including replay with a reconstructed controller. This is unit evidence, not a live-cluster recovery qualification.

These primitives are not wired into deployment requests and do not yet provide end-to-end recovery. Remaining integration must persist the activation token before candidate writes, establish the initial inactive Service, exclude legacy stable-resource writers during migration, check candidate readiness, coordinate route/port changes, restore the predecessor through a newly fenced token and complete the request journal only after compensation. Capacity accounting must cover overlapping candidate and predecessor pods. Cleanup must use generation identity and object preconditions, never app-name deletion of a potentially newer generation. Verify delayed writes, server restart and lost responses against a real API server before enabling this path.

### Candidate readiness and durable checkpoints

`ActivatePreparedCandidate` now links prepared candidates to the conditional Service switch. It requires matching app/request identity, the exact candidate specification, a current observed generation, all requested replicas updated/ready/available, and the expected candidate NetworkPolicy. It performs no candidate repair or stale-token refresh. Port changes are rejected until route coordination is implemented. Successful traffic selection is not itself proof of public HTTPS readiness.

Candidate replay comparison normalizes known Kubernetes defaults and ignores only the controller-managed deployment revision annotation. Changed application configuration, extra containers, selectors, security settings and policy content remain rejected. Focused unit checks passed; actual-cluster qualification remains outstanding.

Core schema 10 adds runtime activation checkpoints linked to the durable request journal. The repository saves immutable intent before runtime mutation and supports conditional stage transitions through prepared, fenced, activated, recovering and withdrawn. Saved JSON objects are bounded to 64 KiB; the coordinator must only include non-secret resource identities and traffic targets. Completed requests freeze checkpoints. Tests verify persistence across reopen, conflicting intent/gate rejection and terminal freeze.

The checkpoint repository and readiness switch are implemented. An internal coordinator now connects them, but the public deployment handlers do not invoke it yet. End-to-end stuck-request recovery, initial routing setup, coordinated port changes, rollout capacity, cleanup and live qualification remain incomplete. No live database migration has been performed.


### Candidate coordinator integration

`AdvanceCandidateOperation` now connects the deployment request journal, activation checkpoints and isolated runtime operations. It validates the exact pending request and immutable secret references; captures and saves the predecessor target before fencing; records the fenced gate before preparing pods; and records the activation receipt. Fenced requests may resume readiness checks with the same saved token. Activated checkpoint replay is read-only. A missing fence receipt remains unresolved instead of acquiring a fresh token. Runtime responses are validated before being recorded; post-write receipts use a bounded five-second context independent of caller cancellation.

Runtime prerequisites now admit overlapping capacity, create only immutable environment/pull secrets, and can create an inactive initial Service without selecting any candidate. Existing Services are never overwritten by bootstrap. These helpers do not create a public Ingress or activate a site.

The normal deployment RPC remains unchanged. The coordinator requires its caller to hold the app mutation lock and complete prerequisite preparation first. Outstanding integration includes fenced rollback, recovery after ambiguous receipts, legacy-writer exclusion, application/route records, selected-release status, deletion/cleanup and real API qualification. Do not enable this path merely because isolated coordinator checks pass.

The candidate status reader now follows the owned Service's exact generation selector and requires one matching owned Deployment. Recovery operation IDs may differ from the generation being served. Missing or malformed candidate routing cannot fall back to an old healthy Deployment. This read-only helper is not yet wired into the ordinary status RPC.
