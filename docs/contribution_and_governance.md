# Contribution & Governance

Contributions are welcome. Follow these guidelines:
- Fork the repo and open a pull request against `main`.
- Sign off every commit with `git commit -s` (see DCO section below).
- Write all code in Go. Use C only in the `av/` binding layer when interfacing with libav* libraries.
- Include documentation and example updates alongside code changes.
- Add or update tests to cover your changes. Run `go test ./...` before submitting.

## Developer Certificate of Origin (DCO)

All contributions must include a `Signed-off-by` line in the commit message, certifying that you wrote or have the right to submit the code under the project's LGPL-2.1-or-later license. This is the [Developer Certificate of Origin v1.1](../DCO).

Add the sign-off automatically with `git commit -s`:

```
git commit -s -m "av: fix frame leak in decode path"
```

This appends a line like:

```
Signed-off-by: Jane Doe <jane@example.com>
```

If you forget, amend the commit before pushing:

```
git commit --amend -s
```

By signing off you are agreeing to the terms of the [DCO](../DCO). Every commit in a pull request must carry a valid `Signed-off-by` line; PRs that fail the DCO check will not be merged.

## Bug Reports

File bug reports as GitHub issues. Search existing issues first to avoid duplicates.

**Describe the problem:**
- What happened? What did you expect to happen instead?
- Is this a crash, incorrect output, performance problem, or config validation error?
- Exact steps to reproduce the issue.

**Include diagnostic info:**
- Output of `mediamolder version` (prints MediaMolder version, FFmpeg version, license level, and FFmpeg build configuration).
- Your pipeline JSON config, **minimized** to the smallest config that still reproduces the issue.
- Output of `mediamolder inspect <config.json>`.
- Operating system and version (e.g. Ubuntu 24.04, macOS 15.4).
- Hardware details if relevant — especially GPU model when using hardware acceleration (CUDA/VAAPI/QSV).

**Attach supporting files:**
- All error messages and log output.
- A sample input file or link to one (if the issue depends on specific media).
- A copy of the incorrect output (if relevant).

## Feature Requests

Feature requests can also be submitted as GitHub issues. Describe the use case and, where possible, include an example pipeline JSON config or FFmpeg CLI command showing what you'd like to achieve.

## Security Vulnerabilities

Do **not** file security vulnerabilities as public issues. Instead, report them via [GitHub Security Advisories](https://github.com/MediaMolder/mediamolder/security/advisories/new) so they can be triaged privately. See [security.md](security.md) for MediaMolder's security model.