# Self-hosted CI runner

An ephemeral, repo-scoped GitHub Actions runner in a container, deployed on
our own hardware through Dokploy. `.github/workflows/ci.yml` sends **pushes to
main** here and leaves **pull requests on GitHub-hosted runners** — treeline is
a public repo, so a fork PR is untrusted code and must not run on hardware we
own. Nothing you do here changes that split.

## Why ephemeral

`EPHEMERAL=true` means a container accepts one job, then exits; Docker's
restart policy brings up a fresh one that re-registers. No job inherits a
dirty working tree, a leftover process, or anything written outside the
volumes from the job before it.

The one thing deliberately kept between jobs is `/opt/hostedtoolcache`, so
`actions/setup-go` doesn't redownload the Go toolchain every time.

## Deploying in Dokploy

1. **Create the access token.** A fine-grained PAT scoped to
   `markcipolla/treeline` with **Administration: Read and write** — that
   permission is what allows minting runner registration tokens. (A classic
   PAT with `repo` also works, but grants far more than this needs.)

2. **New application → Docker Compose**, pointed at this repo, with the
   compose path `ci/runner/docker-compose.yml`. The build context is this
   directory, so the `Dockerfile` beside it is what gets built.

3. **Set the environment variable** `ACCESS_TOKEN` to the PAT, as a secret.
   Compose refuses to start without it rather than registering nothing.

4. **Deploy**, then confirm the runner appears idle under
   Settings → Actions → Runners in the repo, carrying the label
   `treeline-docker`.

5. **Point CI at it** by setting the repository variable:

   ```sh
   gh variable set CI_RUNS_ON --repo markcipolla/treeline --body treeline-docker
   ```

   Until this is set the workflow uses GitHub-hosted runners for everything —
   a `runs-on` label that no runner answers leaves runs queued indefinitely
   instead of failing, so the variable is the last step, not the first.

6. **Verify** with a real run:

   ```sh
   gh workflow run ci.yml --repo markcipolla/treeline
   gh run watch --repo markcipolla/treeline
   ```

## Scaling

One container takes one job at a time. For more concurrency, raise the replica
count in Dokploy — each replica registers under its own name from
`RUNNER_NAME_PREFIX`. The workflow is a single job, so one replica is enough
unless several pushes land together.

## Rolling back

Unset the variable and CI returns to GitHub-hosted runners immediately, with
no redeploy:

```sh
gh variable delete CI_RUNS_ON --repo markcipolla/treeline
```

## What the image adds

The base image ships neither of these, and the suite needs both:

- `build-essential` — `go test -race` needs a C toolchain
- `tmux` — the tmux-backed session tests skip without it, quietly shrinking
  the suite. The workflow's "Check optional tools" step warns if it is missing,
  so a regression here is visible rather than silent.
