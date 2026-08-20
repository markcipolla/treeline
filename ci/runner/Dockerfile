# The runner treeline's CI uses for pushes to main. Fork pull requests are
# deliberately kept on GitHub-hosted runners — treeline is public, so a fork
# PR is untrusted code and must never execute here. See
# .github/workflows/ci.yml.
#
# myoung34/github-runner handles registration and the ephemeral lifecycle;
# this layer adds only what treeline's suite actually needs.
FROM myoung34/github-runner:2.336.0-ubuntu-noble

# build-essential: `go test -race` needs a C toolchain, and the image has none
# tmux:            the tmux-backed session tests skip without it, quietly
#                  shrinking the suite (the workflow warns when it is absent)
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential tmux \
 && rm -rf /var/lib/apt/lists/*
