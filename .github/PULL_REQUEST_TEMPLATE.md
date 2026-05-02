<!--
Thanks for the PR!

Subject line follows Conventional Commits — `<type>(<scope>): <subject>`.
Types: feat, fix, docs, refactor, test, chore, perf, build, ci, style, revert.
Append `!` for breaking changes. See AGENTS.md for the full convention.
-->

## Summary

<!-- One short paragraph: what does this change and why. -->

## What changed

<!-- Bullet the substantive changes. Skip the trivial ones. -->

-

## How to test

<!--
Concrete steps a reviewer can run. Example:

```bash
task test:race
task build
sudo ./bin/dockersnap serve &
DOCKERSNAP_REMOTE=http://localhost:9847 ./bin/dockersnap create demo --plugin echo
```
-->

## Checklist

- [ ] `task test:race` passes locally
- [ ] `task vet` passes locally
- [ ] Docs updated if behaviour or interfaces changed (`docs/`, `README.md`, `AGENTS.md`)
- [ ] Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
- [ ] No `Signed-off-by` trailers (this project doesn't require DCO)
