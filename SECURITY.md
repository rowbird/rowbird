# Security policy

## Supported versions

Security fixes go to the latest minor release. When a new major version comes out, the previous
major receives security fixes for six months.

## Reporting a vulnerability

**Do not open a public issue.** Report privately by either:

- email to **security@rowbird.dev**, or
- GitHub's [private vulnerability reporting](https://github.com/rowbird/rowbird/security/advisories/new).

Include the affected version, a description, steps to reproduce or a proof of concept, and the
impact you see. Write in English or Portuguese.

## What happens next

- We acknowledge your report within **3 business days** and keep you informed of progress.
- We confirm the problem, assess its severity and prepare a fix and a release.
- We coordinate disclosure with you. Our target is a fix within **90 days** of the report, sooner
  for severe issues. We publish a GitHub security advisory (with a CVE when appropriate) when the
  fixed release is available.
- We credit you in the advisory unless you prefer to stay anonymous.

Please give us reasonable time to fix the issue before disclosing it, do not access or modify data
that is not yours, and do not degrade other people's instances. Good-faith research following this
policy is welcome and we will not pursue legal action over it.

## Scope

In scope: the Rowbird server, its web UI, the CLI, the official Docker image and the release
artifacts. Examples: authentication or authorization bypass, access across workspaces, secret
disclosure, SQL injection through parameters, writes to a read-only connection, SSRF past the
network policy, XSS, formula injection in generated files.

Out of scope: vulnerabilities in your own database or infrastructure, denial of service by an
authenticated admin, missing hardening on instances configured against the documentation, and
reports from automated scanners without a demonstrated impact.

## Verifying releases

Releases are signed with cosign keyless signing and come with SBOMs. See
[Verify downloads](https://docs.rowbird.dev/install/verify).
