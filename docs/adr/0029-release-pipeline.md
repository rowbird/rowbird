# ADR-0029: Releases with GoReleaser, a distroless non-root image and keyless signing

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

Rowbird ships as a single binary and a container image (docs/spec/08-operations.md,
docs/spec/10-quality-and-community.md). A release must be reproducible from a tag, cover Linux,
macOS and Windows on amd64 and arm64, and let users verify what they download. The project is
developed in a private repository first and published later under `rowbird/rowbird`, so nothing in
the private repository may publish by accident.

## Decision

- **GoReleaser builds everything from one file** (`.goreleaser.yaml`): the web build runs as a
  hook, then six static binaries (`CGO_ENABLED=0`, `-trimpath`, version metadata by ldflags, file
  times from the commit), archives, `checksums.txt`, an SPDX SBOM per archive (syft), the
  changelog from Conventional Commits, and a draft GitHub release that a person publishes.
- **The image is distroless and non-root.** `gcr.io/distroless/static-debian12:nonroot` with the
  binary, user 65532, `/data` created with that owner and declared as a volume, and a
  `HEALTHCHECK` that runs `rowbird healthcheck`. It carries CA certificates and nothing else; time
  zones come from `time/tzdata` in the binary. GoReleaser builds it for linux/amd64 and
  linux/arm64 with buildx and pushes one multi-arch manifest to `ghcr.io/rowbird/rowbird` with the
  tags `X.Y.Z`, `X.Y`, `X` and `latest` (prereleases get only `X.Y.Z-...`).
- **Signing is keyless.** cosign signs the checksums file and the image manifest with the GitHub
  Actions OIDC identity of the release workflow, so there is no signing key to store or leak.
- **Only the public repository releases.** The release workflow runs on `v*` tags and its job is
  guarded by `github.repository == 'rowbird/rowbird'`; a tag in any other repository, including the
  private one where Rowbird was built, does nothing.
- **Snapshots are local.** `make snapshot` runs the same configuration with `--snapshot
  --skip=sign`: everything is built into `dist/` and loaded into the local Docker, nothing is pushed.

## Consequences

- A release is a tag plus a review of the draft notes; nothing else is manual.
- Users verify downloads with `cosign verify-blob` against the checksums and images with
  `cosign verify`, both bound to the workflow identity (documented in the docs site).
- The image has no shell. Debugging uses `docker cp` or a debug sidecar, and every operation the
  image needs (health, backup, restore, user recovery) is a `rowbird` subcommand.
- macOS binaries are not notarized in v1; the docs explain how to allow them or use Docker.
