# Verify downloads

Releases are built by the release workflow of `rowbird/rowbird` on GitHub Actions and signed with
[cosign](https://github.com/sigstore/cosign) keyless signing: the signature is bound to that
workflow's identity, so there is no key to trust or lose. Each release also has a software bill of
materials (SPDX JSON) for every archive.

## Archives

Download the archive, `checksums.txt` and `checksums.txt.sigstore.json` (the signature bundle), then:

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/rowbird/rowbird/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

sha256sum --ignore-missing -c checksums.txt
```

The first command proves the checksums came from the release workflow; the second proves your
archive matches them.

## Images

```bash
cosign verify ghcr.io/rowbird/rowbird:1.0.0 \
  --certificate-identity-regexp '^https://github.com/rowbird/rowbird/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## SBOM

`rowbird_<version>_<os>_<arch>.tar.gz.sbom.json` lists the Go modules compiled into the binary. Feed it to a scanner such as `grype sbom:./rowbird_1.0.0_linux_amd64.tar.gz.sbom.json`.
