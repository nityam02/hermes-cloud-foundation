# ADR 0004 — Render root modules as JSON; ship version-locked modules inside the CLI

- Status: Accepted (2026-11-18)

## Context

The CLI must turn a normalised spec into something the engine can run. The options are text templating of
HCL, generating HCL with a library, or generating the JSON configuration syntax that both engines accept natively
(`*.tf.json`). The CLI version and the module version must not drift apart.

## Decision

- Hermes renders `.hermes/build/<environment>/main.tf.json`. It contains the `terraform` block (required version,
  exactly pinned providers, backend), the provider blocks, one `module "platform"` call with every contract
  variable, and pass-through outputs. Output is deterministic: keys are sorted, and the digest is the SHA-256 of
  the rendered files.
- The HCL modules are embedded in the binary (`go:embed`) and extracted to `.hermes/modules/<cli-version>/`.
  Extraction never overwrites a different version silently. `--modules-dir` lets contributors point at a working
  tree.
- The rendered root is an ordinary engine root module. Users can inspect it, run the engine on it directly, or
  commit it if their process requires it.

## Alternatives considered

- **HCL text templates**: these are fragile (quoting and escaping), hard to test, and invite logic in templates.
- **Remote module sources (git/registry)**: these need network access at init and allow version drift between
  the CLI and the modules. They remain possible for users who call the modules directly.

## Consequences

- Golden-file tests cover rendering for every example.
- The JSON syntax is less pleasant to read than HCL; `hermes render --print` pretty-prints it.
