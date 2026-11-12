# Contributing

Thank you for considering a contribution to govue.

## Proposing a change

Open an issue before starting a non-trivial change. Describe the problem and the approach, and
wait for discussion before writing code. A trivial fix, such as a typo or a small documentation
clarification, can skip straight to a pull request.

## Commit and pull request conventions

Commit subjects and pull request titles follow
[Conventional Commits](https://www.conventionalcommits.org). release-me parses them to decide the
next version.

Branch names follow `<type>/<issue-number>-<slug>`, for example `fix/52-csrf-token-rotation`.

## Developer Certificate of Origin

Every commit needs a `Signed-off-by` trailer. Add it with `git commit -s`.

```text
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

Signing off means you certify that you wrote the change, or otherwise have the right to submit it
under this project's license, Apache-2.0.

DCO enforcement runs in CI: the `dco` workflow checks every commit in a pull request and fails
the check when one lacks a valid sign-off.

## Local development

```sh
task setup   # install the pinned toolchain through mise
task check   # every check, one after another
```

`task --list` shows one target per check. `task check` is the full gate CI runs.

A contribution that only touches `web/` does not need the Go toolchain checks to pass locally.
Loop on `task web:test`, `task web:lint`, `task web:format` and `task web:types` instead, and
`task web:fmt` to write the formatting `web:format` only reports. `task web:build` checks that the
package builds standalone. Run `task check` before opening the pull request, since that is what
CI and the final gate run.

## Design decisions

The design and the reasoning behind it live in
[docs/concepts/govue.md](docs/concepts/govue.md). This repository keeps architecture decision
records sparse: `docs/adr/` does not exist and stays absent until a decision is genuinely
contested and expensive to reverse. Read the concept doc before proposing a change to the design.
