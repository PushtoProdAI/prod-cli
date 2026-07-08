<!-- Thanks for contributing to prod! Keep this short — a sentence or two is fine. -->

## What & why

<!-- What does this change, and what problem does it solve? Link any issue: Fixes #123 -->

## How to test

<!-- The command(s) a reviewer can run to see it work. -->

## Checklist

- [ ] `make check` passes (from the repo root — build, `go test ./...`, gofumpt, lint, gitleaks)
- [ ] Added/updated tests (use the `llm.Client` mock — never hit a real LLM)
- [ ] If I added a `StatusWriter` event, I implemented it in **all** writers + updated the parity test
- [ ] If this needs a live cloud to fully validate, I said so above
