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

### Routing recovery and candidate retirement

`RestoreCandidateTraffic` now records a deterministic recovery identity before changing the Service. It fences the abandoned activation token, restores the immutable predecessor target and records a routing-withdrawn checkpoint. A lost fence or restore response can be resolved under that same recovery identity; recovery never issues a new candidate activation. An unrelated newer operation or recreated Service is rejected. Completed routing recovery replays read-only.

`RecoverCandidateOperation` combines routing restoration, candidate retirement, observed pod removal and exact predecessor readiness. A new app must have captured the explicit inactive bootstrap target. Existing predecessors must match their saved configuration, selector, service port, immutable secret references and fully observed replica status. A routing-withdrawn checkpoint alone does not complete the deployment request.

Retirement creates or conditionally updates a permanent zero-replica Deployment tombstone. It does not delete the candidate identity, NetworkPolicy or Secrets, so a delayed create-only writer encounters an existing object instead of recreating an abandoned generation. Retirement verification also waits for all matching Pod objects to disappear. This proves the observed state at the check, not that Kubernetes can never briefly report another terminating object.

Focused checks cover lost replies, late activation rejection, delayed create rejection, changed runtime identity, drain waiting and predecessor readiness. Runtime/server/database checks and relevant vet checks passed. These are local simulated-runtime checks. The public deployment/recovery API, request/app/route finalization, exclusion of legacy stable-resource writers, status integration and real-cluster qualification still need connection before enablement. Live workloads remain unchanged.

### Existing runtime status and legacy mutation protection

The ordinary runtime `Status`, `StatusDetails` and `RuntimeStatus` methods now follow candidate-selected routing when candidate markers are present. Running-node reporting uses the selected generation's selector. Candidate integrity and API errors remain visible and cannot fall back to an unrelated healthy legacy Deployment. An activated first deployment remains recognizable even while its bootstrap annotation is retained. Genuinely legacy Services preserve the previous status behavior.

Legacy reconcile and delete now reject candidate-managed apps before resource writes. Stable Service updates/deletes also check candidate markers immediately before mutation; Service deletion includes UID and resourceVersion preconditions. Malformed or empty candidate markers still block legacy mutation. Focused compatibility and mutation-free rejection checks passed.

These guards prevent new legacy calls through the updated runtime. They do not cancel already-running calls or older server binaries. Deployment rollout must stop and drain old writers before candidate opt-in. Existing candidate-independent live Services are not changed by this source update. The candidate deployment/recovery API and transactional app/route/request finalization remain to be connected.

### Atomic candidate bookkeeping

Local schema 11 binds each candidate request to immutable app and deployment IDs. Binding creates the pending deployment in the same transaction, preserves an active predecessor, and keeps a first deployment hidden as a deleted provisioning row until activation is finalized. Reusing a deleted app requires its existing ID. Changed IDs, configuration or predecessor snapshots are rejected; exact binding replay remains read-only after completion.

`CandidateFinalizationRepository.Finalize` now commits the app configuration, route record, deployment status and saved request response in one SQLite transaction. It checks the bound identities, pending deployment, unchanged predecessor, matching checkpoint intent and the exact gate observed by the caller. Applied requests require an activated checkpoint; withdrawn requests require a withdrawn checkpoint. Withdrawal preserves the existing predecessor and its route. Terminal replay returns the saved reply, and opposite outcomes are rejected. The older request-only completion method cannot complete bound candidates.

This is a persistence boundary, not independent runtime proof. Its caller must hold the app mutation lock and verify current candidate readiness or full recovery, including candidate retirement and predecessor readiness. Route records remain pending until route health is observed. The public candidate deployment/recovery handlers still need this wiring, runtime route coordination and controlled fleet qualification before enablement. No live migration or deployment was performed.

Focused database checks cover initial/update completion, deleted app reuse, predecessor preservation, stale evidence rejection, immutable replay, legacy completion exclusion and injected transaction failures. A failure at the final journal write rolls back the app, route and deployment changes together. Database, server, ingress and server-command package checks passed.

### Runtime completion and route coordination

`CompleteCandidateOperation` connects runtime evidence to the atomic finalizer. An applied operation must still select the request's exact generation at the saved activation gate, and the selected workload must match its saved configuration and be ready. A withdrawal runs traffic recovery, waits for retirement and predecessor readiness, then restores the predecessor's route or removes the initial attempt's route. Readiness waits produce no terminal result. Route failures, changed traffic and database failures cannot produce a success receipt. Terminal request replay reads the saved response without runtime writes.

The route helper touches only the owned Ingress after checking the exact Service gate, selector and ports. Ingress updates use the existing resource identity/version; deletion uses UID and resourceVersion preconditions. Completion rechecks the selected Service after route reconciliation before committing the database receipt. This does not make Kubernetes Service and Ingress changes atomic, and it is not proof that public DNS or HTTPS is ready. Cross-resource late-write handling still needs real API qualification and must not be treated as solved by these local checks.

These internal coordinators are not yet invoked by the public deployment API. The next integration must expose explicit advance/recovery operations, preserve read-only request lookup, prepare and bind requests before runtime mutation, and keep candidate enablement off until old writers are drained and the complete path is qualified. Existing live deployments are unchanged.

### Explicit operation API and CLI

The additive admin RPCs `AdvanceDeployRequest` and `RecoverDeployRequest` now operate on prepared candidates. CLI equivalents are `deployer apps advance APP REQUEST_ID` and `deployer apps recover APP REQUEST_ID`; JSON output uses the same request metadata as `apps request`. Recovery abandons the attempted release and restores its saved predecessor. Lookup remains read-only.

Server operations default off. `DEPLOYER_ENABLE_CANDIDATE_OPERATIONS=true` enables these handlers only when binding, checkpoint, finalizer and runtime dependencies are configured. This is not a production enablement instruction: candidate creation/preparation is not yet exposed through normal deployment submission. Both actions require admin authentication and the per-app mutation lock. Missing bindings/checkpoints, ambiguous advance stages, changed identities and opposite terminal outcomes are rejected. Advance resumes only fenced or activated checkpoints; a not-ready candidate returns pending metadata. Recovery may remain pending while retirement or predecessor readiness is incomplete. Completed outcomes replay without runtime writes.

Focused handler checks cover anonymous/agent denial, default-off behavior, unbound and ambiguous requests, advance/recovery completion, terminal replay and read-only lookup during readiness waits. Client checks verify additive RPC dispatch and recorded response validation. Server, configuration, client and CLI suites plus relevant vet checks passed. The server/CLI changes remain source-only. Durable candidate request creation/preparation, rollout routing qualification and controlled fleet enablement are still required.

### Candidate submission integration

With candidate operations explicitly enabled, tracked hosted submissions now enter the candidate path through `DeployApp`. Legacy deployments and the default-disabled configuration retain their prior behavior. The candidate creator journals the request, captures its predecessor, and binds the app and pending deployment in one transaction before runtime writes. Replays return the original binding despite newly proposed IDs; changed input and unbound legacy requests are rejected. Active/deleted app IDs are preserved, and unsupported predecessor/port changes are rejected before insertion. Domain admission checks existing routes and other pending requests in the same transaction.

Preparation resolves versioned dependencies and creates only an inactive initial Service. If no checkpoint exists, an already-bound request may safely repeat that preparation; a prepared checkpoint with an ambiguous fence is still not blindly replayed. `apps advance` can resume after a dependency-preparation failure. Deploy returns an explicitly pending deployment until the candidate is ready. The CLI explains how to advance or inspect it. A first app remains hidden from active app listings until the atomic completion commit; updates retain their predecessor configuration while pending.

Connected local tests use a real database with a simulated runtime to cover submit/wait/advance, stable deployment IDs, changed-input rejection, interrupted preparation, initial withdrawal and preservation of an existing app during a failed update. Relevant database, runtime, server and CLI checks passed. This is still not real-cluster qualification. Fleet-worker scheduling of advance/recovery, candidate-aware deletion/cleanup, cross-resource late-write handling and controlled enablement remain before live Docker hosting. The live flag remains off and no customer workload or database was changed.

### Recovery before the first checkpoint

Recovery now handles an atomically bound pending request whose dependency preparation stopped before saving activation intent. It may create the namespace and the request's inactive bootstrap Service, but never resolves environment/registry values, admits workload capacity or starts candidate pods. For an existing app, the selected predecessor must match its saved configuration and be ready before recovery intent is saved. An unrelated initial Service is rejected.

The resulting intent enters the ordinary recovery flow: fence traffic, preserve the inactive/previous target, retire the candidate generation, observe drain/readiness, reconcile the route and finalize the request atomically. A lost preparation response remains retryable under the same request identity. Focused tests verify dependency failure followed by withdrawal, no candidate workload preparation, terminal replay and refusal to adopt another initial Service. Relevant package checks and vet passed.

Still outstanding before enablement: missing-request ambiguity after a lost initial submission, retry/cleanup of initial withdrawn Service identities, candidate-aware deletion, old generation resource cleanup and real API qualification of delayed cross-resource writes. No live resources or flags changed.

### Reusing a withdrawn first deployment

An initial deployment that has been fully withdrawn can now be followed by a
new request ID for the same app. The coordinator requires the old terminal
withdrawal, both bindings to the same deleted app, the exact saved recovery
Service gate and inactive target, and retirement with no matching pods. It then
rebinds the existing Service through a resourceVersion-checked update, preserving
its UID. The new request may select a different service port because neither
inactive selector serves a workload.

The update changes the bootstrap annotation, selector, ports and operation fence
together. Its deterministic bootstrap fence differs from the old recovery fence,
so a delayed recovery cannot recapture the new Service identity and overwrite
its target. A lost update reply is recognized on the next attempt by the new
request's exact inactive target. This also permits abandoning a new request that
failed dependency preparation before its first checkpoint.

This addresses initial retry only. Candidate-aware app deletion, old-generation
cleanup, withdrawal of an ambiguously submitted request that has no journal row,
and real-cluster cross-resource qualification remain required before enabling
Docker hosting. A missing request lookup is not proof that a delayed submission
cannot arrive; that case still needs a durable rejection/withdrawal record under
the original request identity. Live flags remain unchanged.

Focused checks passed for initial withdrawal followed by a new applied request
with a changed port and preserved app ID, lost reset replies, stale gates,
missing retirement proof, and abandonment before the retry's first checkpoint.
These use a real database with simulated runtime state; the ingress reset also
has a fake Kubernetes client check. The database, server, ingress, CLI and server
command suites pass, as do relevant vet checks and Linux/AMD64 server/CLI builds.
They do not substitute for the outstanding real-cluster qualification.

### Candidate-aware project deletion

`DeleteApp` now removes apps with bound candidate history, including hidden apps
whose initial deployment was withdrawn. It rejects pending requests before any
runtime mutation and enumerates generations by both app name and bound app ID.
Every recorded generation is retired and observed drained before dependencies
are removed. The legacy Deployment is scaled to an observed zero-replica
tombstone, and unknown app-owned Deployments or remaining Pod objects block
cleanup. Other app resources and the namespace remain untouched.

An owned Service marker carries the app ID and deterministic deletion operation.
A single CAS writes the marker, activation fence, inactive selector and ports.
New legacy and candidate submissions check this marker before accepting work;
preflight also rejects deletion in progress. The Service stays in place while
Ingress, legacy policy, PDB, traffic resources and runtime Secrets are removed,
then route records, registry credentials and environment versions are cleared
and the app is marked deleted. The final Service removal uses its UID and
resourceVersion. A retry can resume after the database mark; a deleted app with
a missing Service temporarily recreates only the inactive deletion marker while
checking retained resources again. A missing Service for an active app is
ambiguous and rejected.

This protocol uses the existing per-app mutation lock and requires exactly one
server writer, with older server/worker writers stopped and drained before
rollout. It is not a distributed deletion lock and is not safe to bypass through
direct database writes. No new schema is introduced. Candidate and legacy
zero-replica tombstones remain to prevent delayed creates from restarting old
workloads; candidate policies remain with them. They consume metadata storage,
not running workload capacity. Separate garbage collection must establish that
old writers cannot return before removing those tombstones.

Connected database/server checks cover all-release retirement, admission denial,
preflight denial, hidden apps and retry after the database mark. Fake Kubernetes
checks cover lost atomic marker replies, malformed markers, Pod drain, unknown
workloads, other-app preservation and missing-Service retry. Relevant DB,
server, ingress, CLI and command suites plus vet passed. This remains source-only
and does not constitute real-cluster qualification. Retirement after successful
updates, missing-request ambiguity and controlled fleet enablement remain open.

### Capacity reclamation after successful activation

The Kubernetes candidate path now retires older bound generations only after
its applied request receipt is committed. It verifies the current app config,
selected generation, Service gate and readiness, and skips stale requests or
apps with another pending deployment. Every non-selected terminal generation is
retired; the legacy app Deployment is scaled to a zero-replica tombstone and its
observed state and legacy-selected Pods must drain. The selected candidate,
routes, credentials and environment versions remain available. Restoring an old
release therefore creates a fresh request/generation using the retained inputs.

`AdvanceDeployRequest` now retries this housekeeping for an applied request, as
does replaying an applied `DeployApp` request. Request lookup remains read-only.
Cleanup failure does not undo the committed applied outcome: the mutating call
reports that earlier workload cleanup is pending and should be retried. The
fleet worker advances applied requests before settling the portal job, including
after the original activation deadline or a worker restart. This operation does
not reactivate the release or withdraw an already-applied deployment.

Focused checks cover old-generation retirement, current-generation preservation,
legacy workload drain, read-only lookup, retry without deployment preparation,
stale advance requests and a pending recovery window. Fleet checks verify
retryable cleanup errors after expiry without recovery or another submission.
Relevant package checks passed. These changes remain source-only; missing-request
ambiguity and actual Kubernetes rollout qualification still block enablement.

### Withdrawal before a submission is recorded

`WithdrawDeployRequest` accepts the original YAML and tracked request ID with
withdrawal reporting enabled. The CLI exposes `apps withdraw <app> <request-id>
--file <original.yaml>`. For an absent request, it atomically records the bound
request and a durable no-activation decision before passive runtime recovery.
Existing pending requests acquire the same decision. Normal recovery also saves
that decision before any preparation, so a transient namespace or Service error
cannot let a late original submission activate afterward.

Schema 12 adds `deployment_request_withdrawals`; journal, binding and withdrawal
marker creation share a transaction. Late submit/advance calls reject pending
withdrawal intent, while terminal withdrawal replays return the original result.
An already-applied request is returned as applied and is never reversed by the
new RPC. Original configuration and reporting mode must match exactly. The RPC
requires admin access and enabled candidate operations. It starts no workload
and does not resolve environment or registry secrets.

Focused checks cover interrupted recovery of both absent and existing requests,
late submit/advance denial, terminal replay, applied-race preservation, changed
configuration rejection and atomic rollback on marker insertion failure. This
is source-only. Fleet integration of the missing-request path and actual cluster
qualification remain before production enablement. Upgrade the server before
using the new CLI command; older servers do not support this additive RPC.

### Real Kubernetes lifecycle check (September 25)

The opt-in `TestCandidateRealClusterLifecycle` runs with a new generated
namespace and temporary database. It requires an explicit kubeconfig and pinned
ARM64 HTTP image through `DEPLOYER_CANDIDATE_CHECK_KUBECONFIG` and
`DEPLOYER_CANDIDATE_CHECK_IMAGE`. It leaves existing app namespaces and the live
server database untouched and deletes only its own namespace with a UID guard.

The check exposed two API default differences which fake clients did not add:
an empty pod security context and TCP Service port protocol. Candidate comparison
now accepts the empty default security context while preserving explicit security
changes; candidate Service generation sets TCP explicitly, and restored-target
comparison normalizes only an omitted TCP default. Legacy manifest rendering is
unchanged, with compatibility checks still passing.

On the VPS/Pi cluster, the corrected check passed missing-request withdrawal,
initial retry/publication on both ARM64 workers, unhealthy update recovery with
prior-release health, rejection of a withdrawn request advance, and project
removal with zero remaining app Pods (36.39 seconds). Image used:
`nginxinc/nginx-unprivileged@sha256:4714e0b1b2577eaa1a6131d07c958b67f0eb68e6d0521e90c6e5287db8cf0bc5`.
This is real runtime evidence but does not establish private registry rotation,
public DNS/HTTPS, portal end-to-end behavior or delayed independent writer
isolation. Those checks and the coordinated production upgrade remain. No live
application binaries, feature flags or database schemas were changed.

### Public HTTPS lifecycle (September 25)

The candidate route now creates the proxy retry middleware and transport settings
referenced by its Ingress/Service, then checks the Service gate again before
publishing the route. TLS-enabled routes also ensure the configured ACME issuer.
Previously the candidate path skipped legacy resource reconciliation without
replacing those routing dependencies, so a healthy workload alone could not
establish public routing. Focused route checks cover the referenced resources.

The opt-in cluster test additionally accepts an isolated
`DEPLOYER_CANDIDATE_CHECK_DOMAIN` (`launchstead-check-*.0xivanov.dev`) and explicit
`DEPLOYER_CANDIDATE_CHECK_ACME_EMAIL`. With these set, it checks a trusted HTTPS
certificate, HTTP 200 and the expected nginx body before and after update
recovery. It follows no redirects and does not disable certificate validation.
The test hostname must be newly reserved for this check; do not use an existing
customer hostname. Namespace deletion does not remove external DNS, so delete
only the created DNS record by its saved ID after the run.

The live-cluster check passed in 60.09 seconds using a temporary DNS-only record
at `launchstead-check-20260925.0xivanov.dev`; an independent request from the Mac
also confirmed the certificate, response and expected content. The run covered
initial publication across both Pi workers, recovery with HTTPS still working,
and project deletion. The temporary namespace and DNS record were cleaned up.
Private registry credential rotation, portal end-to-end rollout and independent
delayed-writer checks remain. Candidate binaries are still not installed in the
production services.
