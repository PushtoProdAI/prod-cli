# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Report them privately through GitHub's [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)
(the **Report a vulnerability** button under this repository's **Security** tab),
or by email to `security@pushtoprod.ai`. <!-- TODO: confirm this address routes to the team -->

Please include:

- a description of the vulnerability and its impact,
- steps to reproduce (a proof of concept if you have one),
- affected version(s) / commit, and
- any suggested remediation.

We aim to acknowledge reports within **3 business days** and to provide a
remediation timeline after triage. Please give us a reasonable window to
release a fix before any public disclosure.

## Scope

`prod` runs on your machine and acts with **your own** cloud and LLM
credentials — it has no backend and transmits those credentials to no one but
the target platform's own APIs. Reports we especially want to hear about:

- credential or token leakage (logs, history at `~/.prod/`, error output),
- command or template injection reachable from project input or natural-language
  prompts,
- unsafe handling of files written during a deploy.

## Supported versions

Until a `1.0.0` release, security fixes land on the latest release and `main`.
