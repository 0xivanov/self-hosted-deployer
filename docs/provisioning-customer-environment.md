# Dedicated customer environment provisioning

`scripts/provision-hosting-environment.py` installs an operator-managed customer control plane on a blank Debian or Ubuntu VPS supplied by the operator. It never purchases a VPS or adopts an existing deployer/k3s installation. This is a base-environment workflow; it is not a declaration that a newly created environment is ready for customers.

## Inputs and prerequisites

Copy `provisioning-customer-environment.example.json` and replace the example values. Supply an exact deployer release archive SHA-256, a pinned k3s version, the k3s binary SHA-256 and installer SHA-256. Use a reviewed release containing server identity and configurable WireGuard subnet support. Example hashes are placeholders. The deployer archive and k3s binary are verified before installation; the k3s installer runs with downloads disabled. The release installer is installed for future maintenance but is not run during this workflow. Updates are set to `mode=manual`, with no update timers installed or enabled.

For unpublished candidates or private artifacts, set optional `release.base_url` to an HTTPS directory URL. The asset name is appended to this URL and the same mandatory SHA-256 verification applies. Embedded credentials, query strings and fragments are rejected. Omitting this field preserves the GitHub release URL.

Each environment requires distinct private IPv4 WireGuard, pod and service ranges, a resource budget and a dedicated management DNS hostname/port, normally 7443. The hostname must resolve to the VPS. Port 443 is reserved for application ingress. Configure the provider firewall to admit the required SSH, HTTPS management, application HTTP/HTTPS and WireGuard UDP traffic. Kubernetes management listens on the WireGuard address. No connectivity is established between customer environments.

Before planning, place a CA-trusted TLS certificate chain and matching private key under `/etc/ssl/` on the target. The manifest names these remote paths; it contains no certificate key material. The key must be root-owned mode 0600 and must not be a symlink. Planning verifies the certificate hostname, remaining validity and key match. Arrange certificate renewal separately. Verify and save the target SSH host key before running the workflow; unknown or changed host keys are refused. SSH uses an existing key or agent and noninteractive sudo when the SSH user is not root. `ssh.identity_file` refers to the local SSH private-key **path**, never its contents.

Maintain an environment registry containing every existing environment's actual CIDRs, including legacy networks. Use `[]` only when no environments exist. The command locks this registry while validating and applying, rejects duplicate IDs and overlapping ranges, and adds a record only after successful application. A list or an object with an `environments` list is accepted; other object metadata is retained.

```json
{"environments":[{"environment_id":"existing-customer","wireguard_cidr":"10.82.0.0/24","pod_cidr":"10.182.0.0/16","service_cidr":"10.183.0.0/16"}]}
```

## Plan and apply

The plan runs read-only host and TLS checks and saves the generated commands in a private local record:

```sh
python3 scripts/provision-hosting-environment.py customer-a.json plan \
  --registry environments.json --output-dir provisioning-records
```

An explicit apply installs only on a host that passed the same checks:

```sh
python3 scripts/provision-hosting-environment.py customer-a.json apply --apply \
  --registry environments.json --output-dir provisioning-records
```

The workflow generates fresh WireGuard keys, persists the interface across reboots, configures k3s CIDRs and cluster DNS, creates the application's namespace and quota, binds the server to a persistent identity, enables verified management TLS, checks readiness and the TLS handshake, and creates an initial encrypted platform database backup. The local install record identifies completed steps and any failed step. Bootstrap credentials are saved in a separately created 0600 file, including when a later stage fails. Output directories are checked before remote mutation. The default `provisioning-records/` directory is ignored by Git.

A partially completed installation is deliberately refused on rerun. Inspect its private record and repair it explicitly, or replace the disposable VPS. The workflow does not roll back OS package installation, erase a host, or reset an existing database. Preserve recovery keys and credentials before replacing a host.

## Remaining environment qualification

The record explicitly lists worker onboarding, application ingress TLS, offsite backup scheduling, restore drills, external alerts and customer data coverage as pending. Only `backup.scope=platform-database` with `destination=local-platform-backup` is accepted here. Use the existing backup, external monitoring and onboarding runbooks to finish these tasks. The initial local backup is not offsite protection, and it excludes application data.

Local tests execute generated shell commands with real file operations, checksums and archive extraction in an isolated directory while substituting host services and downloads. They verify configuration and a rejected checksum before binary installation. They do not qualify boot behavior, real firewall routing or a fresh VPS. Those require the disposable customer-environment rehearsal in the POC plan.

The [Mac VM rehearsal](mac-qualification.md) now covers real Ubuntu systemd/k3s provisioning, reboot readiness and synthetic platform backup recovery outside the guest. Provider firewall routing and full customer onboarding remain separate qualification tasks.

Worker installation now persists the hub address returned by authenticated worker bootstrap into an existing agent environment file. This prevents heartbeat checks from retaining the legacy `10.8.0.1` hub for a customer with a distinct WireGuard range. Direct `join-k3s` use without an environment file remains supported; configure `DEPLOYER_WIREGUARD_HUB_IP` for the eventual heartbeat service in that case. The installer supplies the environment path explicitly. Restart a running agent after rejoining to reload its environment.

The agent installer also enables `wg-quick@wg0.service` for subsequent boots and installs an additive k3s-agent drop-in requiring WireGuard first. It does not start the WireGuard unit immediately because enrollment has already raised the interface. The [two-VM worker rehearsal](mac-worker-qualification.md) records actual enrollment, restart and traffic checks.
