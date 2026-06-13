---
description: Conventions for AI-assisted edits in MediaMolder
applyTo: "**/*.go,**/*.ts,**/*.tsx,**/*.json,**/*.md"
---

Mediamolder is an improved version of the ffmpeg interface and media graph builder / execution engine. It started as a Go port of FFmpeg's C code. Always read and understand (and to the extent possible, port from C to Go) the FFmpeg code for each feature and structure. Consider the FFmpeg code to be the most mature, reliable, debugged version of the logic needed to construct and run a media processing graph. Our goal for the Go code is to be as reliable, accurate (bug free), and secure as the FFmpeg code while implementing new features that improve flexibility, observability, and usability.


## Documentation
- Update `README.md`, `docs/gui.md`, `docs/architecture.md`, and
  `CHANGELOG.md` whenever public behaviour or APIs change.
- For significant features or algorithms, add an explanatory page under
  `docs/` and link it from the README.
- Do **not** add docstrings, comments, or type annotations to code you
  did not otherwise modify.

## Tests
- Bug fix: add a regression test that fails before the fix.
- New feature: cover typical and edge cases.
- Run the targeted package for fast feedback, then `go test ./...` (and
  `cd frontend && npm test` if the GUI changed) before committing.
- Pre-existing failures unrelated to your change must be reported, not
  silently "fixed" or skipped.

## Formatting & lints
- `gofmt -s` and `goimports` clean (struct tag alignment matters — let
  the formatter run on save).
- Frontend: `tsc --noEmit` and `eslint` clean; resolve every entry in
  the VS Code Problems panel.
- Never bypass with `--no-verify`.

## Cross-cutting invariants
- Pipeline schema: changes to `pipeline.Config` / `Output` / `Input`
  require matching updates to `schema/v1.0.json` and `schema/v1.1.json`
  (enforced by `TestSchemaSyncWithGoStructs`).
- Backend ↔ frontend types: `pipeline/*.go` public types mirror
  `frontend/src/lib/jobTypes.ts`.
- Implicit-encoder pass lives in both `pipeline.expandImplicitEncoders`
  (`pipeline/handlers.go`) and `materializeImplicitEncoders`
  (`frontend/src/lib/jsonAdapter.ts`) — keep them in sync.
- CGO build tags: keep `av/cgo_flags.go` and `av/cgo_flags_static.go`
  (`//go:build ffstatic`) consistent.
- Frontend changes require `make build-gui-static` before the embedded
  binary reflects them.

## Commits
- Conventional Commits: `feat(scope): …`, `fix(scope): …`,
  `chore(scope): …`, `style(scope): …`, `docs(scope): …`,
  `refactor(scope): …`, `test(scope): …`.
- DCO sign-off required: `git commit -s` (adds `Signed-off-by:` trailer).
- Subject ≤ 72 chars, imperative mood, no trailing period.
- Body explains *why* (not *what*); wrap at ~72 chars; use separate
  `-m` blocks for paragraphs.
- One logical change per commit. Reformatting and refactors get their
  own commits.

## Operational safety
- No destructive git operations without explicit confirmation:
  `git push --force`, `git reset --hard`, amending pushed commits,
  rewriting shared history, deleting branches.
- Don't introduce new dependencies (Go modules, npm packages) without
  confirmation; prefer the standard library / existing deps.

# Project Rules - MAXIMUM CONCISENESS

## Response Rules (STRICTLY FOLLOW EVERY TIME)
- Be extremely concise. Use only bullets, code blocks, and unified diffs.
- Output ONLY what was requested. No explanations, no repetition, no "here's what I changed".
- Never add pleasantries, summaries of what you did, or "let me know if you need more".
- Stop immediately after the last required output.
- Use the fewest tokens possible while remaining correct and functional.

## Code Style (Apply Automatically)
- Modern syntax only (ES modules, TypeScript strict mode).
- Single quotes, trailing commas, 2-space indentation.
- Prefer short, clear names. Avoid unnecessary comments.

## Workflow Rules
- For any task >3 steps: propose a short plan first, then implement.
- Reference files with @filename instead of pasting code.
- Always suggest running relevant tests after edits.
- When context is high, proactively suggest `/compact`.

## Context & Efficiency
- Keep all responses minimal by default.
- For new/unrelated tasks, recommend starting fresh with /clear.

# Design Principles

## We will not offer binaries
- MediaMolder implements patented technologies. We cannot and will not offer binaries, or a Docker image.
