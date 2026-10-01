# Retained local application storage

Launchstead can mount one existing filesystem `PersistentVolumeClaim` into an
application. This mode is intended for data that already lives on a specific
worker, such as an encrypted SSD attached to `pi-home`.

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
- the named PVC already exists in `deployer-apps`, is bound, uses filesystem
  volume mode, and has a writable access mode;
- no other Deployment in the namespace mounts the claim.

The rendered Deployment uses the Kubernetes `Recreate` strategy. Kubernetes
must stop the existing Pod before starting its replacement, so two HomePhotos
processes never write to the SSD concurrently.

`deployer nodes remove` and `deployer nodes purge` refuse to remove the pinned
node while either a retained-storage Deployment or a retained local PV
references it. The PV guard remains after app deletion because the claim and
volume remain. Neither guard replaces an independent copy of the data.

## Prepare the encrypted mount

The encrypted filesystem must be mounted before k3s starts. On `pi-home`, make
the agent depend on the mount so kubelet cannot start HomePhotos against an
empty directory on the SD card:

```ini
# /etc/systemd/system/k3s-agent.service.d/20-homephotos-storage.conf
[Unit]
RequiresMountsFor=/srv/homephotos
After=cryptsetup.target local-fs.target
```

Then reload systemd and verify the dependency:

```bash
sudo systemctl daemon-reload
sudo systemctl restart k3s-agent
findmnt --target /srv/homephotos
systemctl show k3s-agent -p RequiresMountsFor
```

The `findmnt` source must be the LUKS mapper, not the Raspberry Pi SD card. Give
the application directory to the UID used by the container. Do not make the
directory world-writable.

## Provision a retained local PV and PVC

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
```

Apply and verify it before deploying the application:

```bash
sudo k3s kubectl apply -f homephotos-volume.yaml
sudo k3s kubectl -n deployer-apps get pvc homephotos-data
sudo k3s kubectl get pv homephotos-data
```

The PVC must report `Bound`, and the PV reclaim policy must report `Retain`.

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
- `deployer delete --yes homephotos` leaves `homephotos-data` bound and
  does not erase files.
- Redeploying the same configuration mounts the retained claim again.
- Do not delete the PVC or PV during ordinary application removal.
- If final data destruction is intentional, stop the application, verify the
  exact PV, PVC, and LUKS device, then use a separately reviewed destruction
  procedure.

This is single-copy storage. Encryption, checksums, PVC retention, and node
guards do not recover photos after SSD loss, controller failure, accidental
file deletion, or corruption.
