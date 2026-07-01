# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub's "Report a
vulnerability" button on this repository's Security tab. Do not open a public
issue for a security report.

Include a description, the affected version or commit, and steps to reproduce.
You can expect an acknowledgement within a few days.

## Scope

This library implements security-relevant middleware (`auth`, `jwt`, `csrf`,
`cors`, `secure`, `ratelimit`). Reports of incorrect enforcement in these are in
scope. Behavior documented as a footgun in the package docs, such as a
permissive CORS origin function, is a configuration choice rather than a library
vulnerability.
