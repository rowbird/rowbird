# Kubernetes example

Plain manifests for one Rowbird instance with SQLite on a persistent volume. They are a starting
point, not a Helm chart: adapt the namespace, storage class, host name and ingress class.

```bash
kubectl create namespace rowbird
kubectl -n rowbird create secret generic rowbird \
  --from-literal=master-key="$(openssl rand -base64 32)"
kubectl -n rowbird apply -f .
```

Keep a copy of the master key outside the cluster: backups cannot be read without it.

SQLite allows a single replica, so the Deployment uses the `Recreate` strategy. To run several
replicas, use PostgreSQL (`ROWBIRD_DATABASE_URL`) and S3 artifact storage
(`ROWBIRD_STORAGE_BACKEND=s3`), and see https://docs.rowbird.dev/operations/scaling.
