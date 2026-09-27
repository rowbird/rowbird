# Kubernetes

There is no Helm chart in v1. [`deploy/k8s`](https://github.com/rowbird/rowbird/tree/main/deploy/k8s)
has plain manifests for one instance with SQLite on a persistent volume: a PersistentVolumeClaim,
a ConfigMap, a Deployment, a Service and an Ingress for ingress-nginx.

```bash
kubectl create namespace rowbird
kubectl -n rowbird create secret generic rowbird \
  --from-literal=master-key="$(openssl rand -base64 32)"
kubectl -n rowbird apply -f deploy/k8s/rowbird.yaml
```

Adapt the host name, the ingress class, the storage size and `ROWBIRD_TRUSTED_PROXIES` (the pod
network range of your cluster) before applying.

## Notes

- **One replica with SQLite.** The Deployment uses the `Recreate` strategy so two pods never open
  the same database. For several replicas, use PostgreSQL and S3 storage; see [Scaling](/operations/scaling).
- **Probes.** Readiness uses `/health/ready` and liveness `/health/live`.
- **Security context.** Non-root (65532), read-only root file system, no capabilities, the default
  seccomp profile. `/data` and an `emptyDir` on `/tmp` are the only writable paths.
- **Shutdown.** Running reports get 30 seconds to finish (`ROWBIRD_SHUTDOWN_TIMEOUT`); the pod's
  grace period is 45 seconds.
- **Server-sent events.** The Ingress disables buffering so live updates reach the browser. Other
  ingress controllers need the equivalent setting; see [Reverse proxies](/operations/reverse-proxy).
- **Backups.** Scheduled backups land in `/data/backups`. Upload them to a bucket with
  `ROWBIRD_BACKUP_S3_*` so they survive the volume.
