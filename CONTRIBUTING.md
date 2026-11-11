# Contributing to xolo-chaos

xolo-chaos is the load and fault-injection bench of [Xolo](https://github.com/xolo-gateway/xolo). It follows the governance, the code of conduct and the
contribution rules of Xolo, described in Xolo's
[GOVERNANCE.md](https://github.com/xolo-gateway/xolo/blob/main/GOVERNANCE.md)
and [CODE_OF_CONDUCT.md](https://github.com/xolo-gateway/xolo/blob/main/CODE_OF_CONDUCT.md).

Issues and pull requests may be written in English or French.

## Before you start

Open a pull request directly for a bug fix, a typo or a documentation
change. For a new feature or a change of behaviour, open an issue first and
describe what you want to do. A problem of Xolo itself belongs in the
[Xolo issue tracker](https://github.com/xolo-gateway/xolo/issues).

## Development setup

You need Go (the version pinned in [`go.mod`](go.mod)), GNU make, Docker
and [k6](https://k6.io/). The Xolo repository must be checked out next to this
one: the Makefile builds Xolo and runs its `cmd/seed` from there.

```bash
go vet ./...
go test ./...
```

## Conventions

- Write code that reads like the surrounding code.
- A new fault profile is documented in the README table of profiles, and
  covered by a test of `cmd/chaos-provider`.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat(scope): subject`, `fix: subject`, etc.

## Developer Certificate of Origin (DCO)

This project uses the [Developer Certificate of Origin](DCO) rather than a
contributor license agreement. By signing off your commits you certify that
you have the right to submit the code and that it can be distributed under the
project license, the [Apache License 2.0](LICENSE). You keep your copyright in
full.

```bash
git commit -s -m "fix: description of the change"
```

Every commit of a pull request must carry the `Signed-off-by` line: a check
refuses the pull request otherwise.

### If you forgot to sign off

Last commit:

```bash
git commit --amend -s --no-edit
```

Several commits:

```bash
git rebase --signoff main
```

Then push again with `git push --force-with-lease`.
