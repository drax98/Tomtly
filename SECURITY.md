# Security

Home Control runs code from strangers (extensions), controls real devices and holds their keys, so security reports matter
more here than in most projects. Thank you for taking the time.

## Reporting

**Don't open a public issue.** Use GitHub's private vulnerability reporting: the **Security** tab of this repository, then
**Report a vulnerability**. Include what an attacker needs (network position, an account and its role, an installed
extension, a marketplace listing), the steps, and what they gain.

You'll get an answer within a week. Once a fix is released, the advisory is published with credit to you, unless you'd
rather not be named.

## Scope

Especially welcome:

- an extension reaching anything it didn't declare and wasn't approved for: hosts, ports, the service's own network,
  files, other extensions' settings, or the service's process
- an account doing more than its role allows, or acting without signing in
- a package installed or updated without a valid approval signature, or a different package than the one approved
- the official marketplace (its service isn't in this repository, but reports about it are welcome here): a member
  reaching the review queue, approving, or uploading to an id they don't own; anything that runs a stranger's code in the
  browser
- secrets (device keys, tokens, extension settings, password hashes) leaving the service

Out of scope: what an extension does with access its owner approved, a service exposed beyond `localhost` without HTTPS
against the README's advice, and denial of service by someone already signed in as an administrator.

## Supported versions

Only the latest release gets security fixes.
