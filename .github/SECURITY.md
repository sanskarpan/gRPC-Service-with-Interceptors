# Security Policy

## Supported versions

Security fixes are provided for the latest released minor version on `main`.

## Reporting a vulnerability

Please report security issues **privately** via GitHub's
[private vulnerability reporting](https://github.com/sanskarpan/gRPC-Service-with-Interceptors/security/advisories/new)
(Security → Report a vulnerability). Do **not** open a public issue for a
suspected vulnerability.

Include, where possible:

- affected component and version/commit,
- a description and impact assessment,
- reproduction steps or a proof of concept.

We aim to acknowledge reports within 3 business days and to provide a
remediation timeline after triage.

## Scope highlights

- Authentication and TLS/mTLS handling.
- Input validation and resource-bound enforcement.
- Supply-chain integrity of released images (SBOM, signatures, provenance).

Development TLS certificates in this repository are self-signed and for local
use only; they are not a vulnerability.
