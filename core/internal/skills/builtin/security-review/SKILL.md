---
name: security-review
description: Review code for security problems - untrusted input, injection, secrets, authorization, unsafe defaults and dependencies - and propose concrete fixes.
---
# Security review

Use this to check a change or a component for vulnerabilities. Report real, exploitable issues with a scenario;
do not pad the report with generic advice.

## Map the trust boundaries
- List every input an attacker can influence: HTTP requests, CLI args, environment, files and archives, database
  rows, messages from other services, model output, web pages, repository content.
- Follow each one with `grep` and `read` to where it is used. Bugs live where untrusted data meets a powerful sink.

## Sinks to check
- **Command execution**: shell strings built from input (use argument arrays, never string concatenation).
- **SQL / NoSQL / LDAP**: string-built queries (use parameters).
- **Paths**: `..`, absolute paths, symlinks and archive entries escaping a folder (resolve, then check the prefix).
- **HTML / templates**: unescaped output (XSS), `innerHTML`, markdown rendering of untrusted text.
- **URLs and network**: SSRF to internal addresses and cloud metadata, open redirects, TLS verification turned off.
- **Deserialization**: unsafe loaders (pickle, YAML full load, Java/PHP object streams), unbounded sizes.
- **Regex and parsing**: catastrophic backtracking, unbounded memory, zip bombs.

## Secrets and data
- Secrets in code, config, logs, error messages, URLs, test fixtures or commit history.
- Sensitive data (credentials, tokens, personal or health data) logged, cached, sent to third parties or stored
  unencrypted. Error messages that leak internals.

## Authentication and authorization
- Every action checks who the caller is and whether they may do this to this object (IDOR).
- Session and token handling: expiry, rotation, comparison in constant time, CSRF protection.

## Configuration and dependencies
- Unsafe defaults: debug on, permissive CORS, wildcard permissions, world-writable files, disabled sandboxes.
- Dependencies: pinned versions from approved internal registries only; note known-vulnerable or unmaintained
  packages. Never suggest switching to a public registry.

## Report
For each finding: severity (critical, high, medium, low), the file and lines, the attack scenario step by step,
the impact, and a concrete fix. Say what you checked and found clean, and what you could not assess.
