# Mac environment recovery rehearsal

This rehearsal restores the synthetic stateless hosting control plane into a new Ubuntu VM, without starting the original environment concurrently. It uses the existing recovery-bundle code to snapshot both SQLite databases and capture configuration. The Mac retains the original VM disk as a fallback, but the replacement receives only the reviewed bundle and the test application's image archive.

## Recovery scope

The bundle includes:

- Consistent platform and k3s SQLite snapshots at their original restore paths.
- Deployer server environment, stable identity, credentials, update policy and binary.
- WireGuard interface configuration and private key.
- k3s configuration, original server token, CA/credentials, server manifests, installed binary and systemd unit.
- `/etc/rancher/node/password`, paired with the original Kubernetes node name.
- The synthetic management TLS certificate/key and lab CA trust.
- The existing synthetic CLI context, which checks the restored server identity.

The test application image is copied separately as an OCI/Docker archive. No application database or persistent volume is part of this stateless fixture. Recovery backend credentials and `/etc/deployer/backup` are excluded; independently held recovery keys are not reconstructed from this bundle.

The original and worker VMs must be stopped before the replacement starts restored services. Both control-plane copies use the same WireGuard IP and credentials. Never run them together. The new guest keeps its Lima hostname but explicitly sets the original Kubernetes `node-name` before first k3s start, preserving the node-password relationship.

## Restore sequence

1. Capture with the existing bundle implementation, check required files did not change, copy into private Mac storage, and verify SHA-256.
2. Prepare a blank disposable Ubuntu guest with the pinned architecture/image and required WireGuard/CA packages. Confirm it contains no existing deployer or k3s installation.
3. Stage the verified archive into a new private directory. Review its paths and restore coverage before installing files. The staging helper does not install services or activate a restored environment.
4. Install the reviewed files with their original normal permission bits. Preserve the k3s token, node password, platform identity, encryption keys and token-hash key. Do not run a fresh bootstrap over restored state.
5. Restore synthetic DNS and CA trust, start WireGuard, then k3s, import the application image, and start deployer-server.
6. Verify node and ingress readiness, application HTTP behavior, and authentication using the original identity-bound context. Worker recovery requires updating its synthetic management DNS to the replacement and checking its existing credentials against the recovered control plane.

## Evidence boundary

This is a local bundle recovery exercise, not a new Backblaze download or an independently measured customer RPO/RTO. Live B2 delivery has separate evidence. The two SQLite snapshots are individually consistent; they are not an atomic snapshot across all platform and Kubernetes state. Application data, public certificate renewal, external DNS cutover, external alert delivery and the complete customer pilot remain separate acceptance checks.

## Staging helper

Run the helper with a trusted expected digest and an absolute destination under a private directory owned by the current user:

```sh
python3 scripts/stage-recovery-bundle.py recovery.tar.gz \
  --sha256 EXPECTED_SHA256 --output /absolute/private/recovery-staged
```

It hashes a private snapshot and parses those same bytes, validates the complete member list before creating the destination, rejects existing output directories, and preserves ordinary file/directory permissions while stripping special permission bits and archive ownership. Links, devices, FIFOs, traversal, duplicate paths and file/directory conflicts are refused. Archives containing legitimate symlinks need a separately reviewed recovery procedure; the helper intentionally does not follow them.

The staging root and implicit parent directories are private. **Do not copy the whole staging tree onto `/`.** Install only the archive's reviewed entries and preserve existing OS parent-directory modes. The first lab activation served the app but this broad-copy mistake broke subsequent nonroot SSH access. That disposable replacement was discarded. The corrected procedure was rerun on a fresh guest, with `/`, `/etc` and `/usr` remaining `0755` and database files explicitly `0600`.

New recovery bundles now archive SQLite snapshots as `0600` independent of process umask. Older archives can contain more permissive snapshot modes; review and tighten database permissions during restore.

## Corrected clean restore, September 9

The replacement started from the pinned blank Ubuntu image, received the verified local bundle and test image, and started the original platform identity and Kubernetes node name. Both SQLite databases passed integrity checks before activation. No bootstrap generated replacement credentials.

The corrected activation returned the control-plane node Ready, Traefik Available, and the hosted app Ready. An HTTP request through ingress returned `hosting-lab-ok`; the original identity-bound CLI context authenticated and listed the original app. A separate subsequent SSH session worked, and `/`, `/etc`, `/usr` and database permission checks passed. The source VM remained stopped throughout.

This includes a retry after a Docker Hub sandbox-image pull timed out. Runtime system images were downloaded normally; the application image came from the saved archive. The rehearsal does not establish registry-independent recovery or a customer RTO target.

## Existing worker recovery

After switching the worker's synthetic management DNS to the replacement, its saved agent credential remained byte-for-byte unchanged. The first attempt could contact the management endpoint but could not use the VPN: the recovered hub had no runtime WireGuard peers. Previously these peers were only populated during worker bootstrap/removal, not server startup.

The server now reloads saved peers before accepting RPCs when worker networking is configured. It uses a bounded context and fails startup on synchronization errors so systemd can retry after the interface becomes available. Servers without configured worker networking retain their previous startup behavior. Running this before the listeners avoids a startup snapshot racing with node enrollment or removal. The existing hub synchronizer excludes removed nodes and removes stale peers.

With the candidate fix installed only in the replacement lab, server startup restored the saved peer. The original worker became Ready without a new join token or re-enrollment. This corrects an actual restart/recovery dependency rather than manually reconstructing peer commands for the test.

A subsequent replacement-host reboot passed: deployer-server, k3s and WireGuard were active, the saved peer was reconstructed, both Kubernetes nodes were Ready, and the app served `hosting-lab-ok` through ingress. The original CLI context still listed the app. A fresh worker inspection reported online, Ready and VPN connected. OS parent-directory permissions remained `0755`.

The active lab copy is now `deployer-poc-restore`; the original `deployer-poc-lab` remains an offline fallback. The worker's synthetic hostname mapping points to the replacement. For subsequent work start the replacement and worker together, never both control-plane copies. The checked-in single-VM app smoke requires the old hostname and is not directly applicable to the replacement without a reviewed update to its target guard.
