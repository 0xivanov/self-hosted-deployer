# Retained local application storage

Launchstead can mount existing filesystem `PersistentVolumeClaim` objects into
an application. This mode is intended for data that already lives on a
specific worker, such as an encrypted SSD attached to `pi-home`. The primary
claim remains in `storage.existingClaim`; up to seven more claims can be listed
in `storage.additionalMounts`.

Launchstead does not create, resize, rebind, or delete the claim. Deleting the
application removes its Deployment and routing resources but leaves the PVC
and PV intact. The operator owns storage provisioning and final destruction.

## Safety contract

A configuration with `storage` is admitted only when all of these conditions
are true:

- `state.mode` is `stateful`;
- `resilience.mode` is `pinned`;
- `deploy.replicas` is `1`;
- `deploy.strategy` is `recreate`, or is omitted and normalized to `recreate`;
- `placement.prefer` contains an exact `node-id` selector;
- every named PVC already exists in `deployer-apps`, is bound, uses filesystem
  volume mode, and has a writable access mode;
- every claim is distinct, and mount paths are clean, absolute, and do not
  overlap;
- no other Deployment or active Pod in the namespace mounts any named claim;
- every PV uses `Retain`, a local volume source, and exact node affinity for
  the configured `node-id`.

The rendered Deployment uses the Kubernetes `Recreate` strategy. Kubernetes
must stop the existing Pod before starting its replacement, so two HomePhotos
processes never write to the SSD concurrently.

`deployer nodes remove` and `deployer nodes purge` refuse to remove the pinned
node while either a retained-storage Deployment or a retained local PV
references it. The PV guard remains after app deletion because the claim and
volume remain. Neither guard replaces an independent copy of the data.

## Prepare the host paths

The encrypted filesystem must be mounted before k3s starts. The audit anchor is
deliberately stored on the Pi root filesystem so it remains independent from
the encrypted photo SSD. Create both directories before provisioning the local
volumes. For the HomePhotos image, UID and GID `65532` are the non-root runtime
identity:

```bash
test "$(findmnt -n -o SOURCE /srv/homephotos)" = \
  /dev/mapper/homephotos-crypt
test ! -L /srv/homephotos
sudo install -d -o 65532 -g 65532 -m 0750 /srv/homephotos
sudo install -d -o 65532 -g 65532 -m 0700 /var/lib/homephotos-anchor
test -d /var/lib/homephotos-anchor
test ! -L /var/lib/homephotos-anchor
if findmnt --mountpoint /var/lib/homephotos-anchor >/dev/null 2>&1; then
  echo '/var/lib/homephotos-anchor must be an ordinary root-filesystem directory' >&2
  exit 1
fi
test "$(stat -c %u:%g /srv/homephotos)" = 65532:65532
test "$(stat -c %a /srv/homephotos)" = 750
test "$(stat -c %u:%g /var/lib/homephotos-anchor)" = 65532:65532
test "$(stat -c %a /var/lib/homephotos-anchor)" = 700
test "$(stat -c %d /var/lib/homephotos-anchor)" = "$(stat -c %d /)"
test "$(stat -c %d /var/lib/homephotos-anchor)" != \
  "$(stat -c %d /srv/homephotos)"
```

The `findmnt` source for `/srv/homephotos` must be the expected LUKS mapper, not
the Raspberry Pi SD card. The two `stat` comparisons keep the anchor on the Pi
root filesystem and prove it is not on the photo SSD. Do not replace the anchor
with a symlink, bind mount, or path below `/srv/homephotos`. Adjust the numeric
identity only when the reviewed container image uses a different UID or GID.

On `pi-home`, make the agent depend on both host paths so kubelet cannot start
HomePhotos against an unmounted SSD or before the root filesystem containing
the anchor is available:

```ini
# /etc/systemd/system/k3s-agent.service.d/20-homephotos-storage.conf
[Unit]
RequiresMountsFor=/srv/homephotos /var/lib/homephotos-anchor
After=cryptsetup.target local-fs.target
```

Then reload systemd and verify both dependencies. Restart the agent only during
an explicit maintenance window because it can interrupt workloads on the node:

```bash
sudo systemctl daemon-reload
sudo systemctl restart k3s-agent
test "$(findmnt -n -o SOURCE /srv/homephotos)" = \
  /dev/mapper/homephotos-crypt
mount_requirements=$(systemctl show k3s-agent.service \
  -p RequiresMountsFor --value)
printf '%s\n' "$mount_requirements" | tr ' ' '\n' | \
  grep -Fx /srv/homephotos
printf '%s\n' "$mount_requirements" | tr ' ' '\n' | \
  grep -Fx /var/lib/homephotos-anchor
```

`RequiresMountsFor` orders the backing filesystems but does not create the
anchor directory. Keep the directory existence, non-symlink, ownership, mode,
and filesystem checks in the node's release preflight. Do not make either
directory world-writable.

## Provision retained local PVs and PVCs

Read the immutable Launchstead node ID with `deployer nodes inspect pi-home`.
Use that value in both the PV node affinity and `deployer.yaml`. The example
below uses `node-home`; replace it with the real ID and choose a capacity no
larger than the mounted filesystem.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: homephotos-data
spec:
  capacity:
    storage: 1800Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: homephotos-local
  local:
    path: /srv/homephotos
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: deployer.io/node-id
              operator: In
              values:
                - node-home
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: homephotos-data
  namespace: deployer-apps
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: homephotos-local
  volumeName: homephotos-data
  resources:
    requests:
      storage: 1800Gi
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: homephotos-audit-anchor
spec:
  capacity:
    storage: 16Mi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: homephotos-anchor-local
  local:
    path: /var/lib/homephotos-anchor
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: deployer.io/node-id
              operator: In
              values:
                - node-home
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: homephotos-audit-anchor
  namespace: deployer-apps
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: homephotos-anchor-local
  volumeName: homephotos-audit-anchor
  resources:
    requests:
      storage: 16Mi
```

Apply and verify both pairs before deploying the application:

```bash
sudo k3s kubectl apply -f homephotos-volume.yaml
sudo k3s kubectl -n deployer-apps get pvc \
  homephotos-data homephotos-audit-anchor
sudo k3s kubectl get pv homephotos-data homephotos-audit-anchor \
  -o 'custom-columns=NAME:.metadata.name,STATUS:.status.phase,RECLAIM:.spec.persistentVolumeReclaimPolicy,PATH:.spec.local.path,NODE-ID:.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]'
```

Both PVCs and PVs must report `Bound`. Both PVs must report `Retain`, the
expected host path, and the same exact Launchstead node ID used in
`deployer.yaml`. The capacity of a statically provisioned local directory is a
scheduling and binding value; it is not a filesystem quota.

## Application configuration

```yaml
name: homephotos
image: ghcr.io/0xivanov/homephotos@sha256:REPLACE_WITH_IMAGE_DIGEST

service:
  port: 8080
  health:
    path: /health/ready

routing:
  domain: photos.example.com
  requireTLS: true

deploy:
  replicas: 1

placement:
  arch: linux/arm64
  prefer:
    - node-id: node-home

state:
  mode: stateful

resilience:
  mode: pinned

storage:
  existingClaim: homephotos-data
  mountPath: /var/lib/homephotos
  additionalMounts:
    - existingClaim: homephotos-audit-anchor
      mountPath: /anchor

hosting:
  version: v1
  readOnlyRootFilesystem: true
  maxReplicas: 1
  resources:
    requests:
      cpu: 250m
      memory: 512Mi
      ephemeralStorage: 1Gi
    limits:
      cpu: "3"
      memory: 4Gi
      ephemeralStorage: 4Gi
```

`routing.requireTLS: true` fails before desired-state or Kubernetes application
resources are written when the server has no application TLS configuration.
With TLS enabled, the Ingress is bound only to Traefik's `websecure` entrypoint
and uses a cert-manager certificate. Configure
`DEPLOYER_INGRESS_ACME_EMAIL` and qualify certificate issuance before the first
HomePhotos deployment. Launchstead never falls back to a plain HTTP route for
an application that requires TLS.

The VPS terminates public HTTPS. Traffic from the VPS to `pi-home` remains on
the private WireGuard-backed k3s network.

## Update, delete, and recovery behavior

- An image or environment update stops the old Pod before starting the new
  Pod. Expect brief downtime.
- Tracked retained-storage deployments use the same Recreate apply path. The
  immutable parallel-candidate rollout is not used for these deployments.
- `deployer delete --yes homephotos` leaves every retained claim, including
  `homephotos-data` and `homephotos-audit-anchor`, bound and does not erase
  files.
- Redeploying the same configuration mounts the retained claim again.
- Do not delete the PVC or PV during ordinary application removal.
- If final data destruction is intentional, stop the application, verify the
  exact PV, PVC, and LUKS device, then use a separately reviewed destruction
  procedure.

This is single-copy storage. Encryption, checksums, PVC retention, and node
guards do not recover photos after SSD loss, controller failure, accidental
file deletion, or corruption.
