<!--
  Thank you for contributing to Pulse.
  Keep the title in Conventional Commits form, e.g. "feat: add Fetcher backfill".
  Fill in every section that applies and delete the ones that do not.
-->

## Summary

<!-- What does this PR change, and why? One or two clear paragraphs. -->

## Related issues

<!-- Link issues this PR closes or relates to, e.g. "Closes #12". -->

## Type of change

<!-- Check all that apply. The prefix must match the PR title (Conventional Commits). -->

- [ ] feat: new feature
- [ ] fix: bug fix
- [ ] perf: performance improvement
- [ ] refactor: code change that neither fixes a bug nor adds a feature
- [ ] docs: documentation only
- [ ] test: adding or fixing tests
- [ ] build / ci: build system, dependencies, or pipeline
- [ ] chore: other maintenance

## Roadmap phase

<!-- Which roadmap phase does this advance? e.g. "Phase 1 - Pipeline". -->

## How was this tested?

<!-- Commands run, scenarios covered, and anything a reviewer should reproduce. -->

- [ ] `go test ./...` passes locally
- [ ] Frontend `tsc --noEmit`, lint, and build pass (if the web app is touched)
- [ ] Verified end to end via `docker compose up` (if applicable)

## Database and migrations

<!-- Delete this whole section if the PR does not touch the schema or queries. -->

- [ ] Migration ships both `.up.sql` and `.down.sql`
- [ ] Round-trip verified: all `up` then all `down` apply cleanly
- [ ] New indexes include an `EXPLAIN ANALYZE` before/after justification
- [ ] `sqlc generate` re-run and generated code committed (if queries changed)
- [ ] No secret-bearing columns exposed to the `playground_readonly` role

## Screenshots or recordings

<!-- For UI changes (dashboard, Playground), attach a screenshot or GIF. -->

## Checklist

- [ ] Title follows Conventional Commits
- [ ] Self-reviewed the diff
- [ ] Docs updated (README) where behavior or design changed
- [ ] CI is green
