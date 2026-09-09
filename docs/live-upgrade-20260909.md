# Live fleet upgrade, September 9

The existing VPS control plane and both Raspberry Pi agents were upgraded sequentially to unpublished candidate `v0.3.1-poc.20260909`, built from commit `f2ab0d98a3a012ce1c2bd0bdcf2fb381616854ae` at `2026-09-09T12:12:54Z`. No release was published. Kubernetes, application configurations, CLI binaries, alert routes and automatic-update settings were not changed.

## Preconditions and artifacts

All three nodes were Ready. The database passed integrity checks and contained one active managed app with zero hosting profiles. All hosts retained manual update policy. Fresh recovery and platform backup jobs completed successfully immediately before the transition, in addition to their successful scheduled runs earlier that day.

| Artifact | SHA-256 |
| --- | --- |
| Linux AMD64 server | `07f302f9d21247658e84bb6344bb0c35c1f6a40c70b60584f90c80f766a78d9c` |
| Linux ARM64 agent | `486613a839c8992e5418264e9bc5f95e28d612a7c48d56aa2988c9c57dedea36` |

Each target verified the candidate checksum, saved the previous binary, configuration and unit definition in a root-only maintenance directory, then used atomic binary replacement. The server also received a consistent local SQLite backup. Service/readiness failures trigger restoration of the saved binary.

## Transitions and observations

The first server attempt restored the previous binary automatically because an HTTPS request from the VPS to its public application address timed out. The candidate had reached management readiness with both VPN peers present. After rollback, the entire deployment/Pod/node baseline and external HTTPS check passed; subsequent public requests from the VPS also passed. The timeout was not reproduced and its cause remains undetermined.

A second server attempt retained the same readiness guards and allowed three bounded attempts for the public request. It passed. Twenty independent HTTPS samples from the Mac during this retry all returned the expected readiness body. This is sampled evidence, not a continuous availability measurement.

The home agent was then upgraded and verified before the yasen agent. Both reported the exact candidate version and commit. Their k3s-agent services stayed active. Original authenticated CLI app/node reads passed against the new server.

Final comparisons showed all 17 Deployment generations, desired replica counts and available replica counts unchanged. All four application Deployment Pods retained their UIDs, readiness and restart counts. All three nodes remained Ready, both active workers were online with VPN connected, the public readiness endpoint passed, and SQLite integrity remained valid. Removed historical worker records were not re-enrolled.

Post-upgrade recovery and platform backup jobs also completed successfully with exit status zero, at 12:18:22 UTC and 12:18:29 UTC respectively. These scheduled-job paths include their existing offsite verification. Automatic-update timers remained disabled and inactive on all hosts.

## Rollback and remaining limits

The successful transitions retain previous binaries/configuration at `/var/lib/deployer/maintenance/20260909-f2ab0d9-attempt2` on each host. The VPS also retains the first attempt at `/var/lib/deployer/maintenance/20260909-f2ab0d9`. Private local build metadata, baseline comparisons, probe results and transition script are under `~/.local/state/deployer/maintenance/20260909-upgrade`; credentials are not checked into Git.

The transition replaced server/agent binaries only. It did not use a published-release download installer, enable hosting profiles on existing apps, exercise public certificate renewal or send a test alert. Hosted apps created with the earlier label defect need a reviewed redeploy to receive the policy selector label; the live legacy fleet had no such opted-in apps. Independent recovery-key custody, provider-level customer isolation and the soak/pilot gates remain open.
