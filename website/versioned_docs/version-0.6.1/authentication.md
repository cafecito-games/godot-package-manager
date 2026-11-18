---
title: Authentication
description: Use private Git repositories and GitHub release assets with gpm.
---

# Authentication

`gpm` delegates credentials to the source mechanism. It does not store tokens
or maintain its own credential database.

## Git Sources

Git sources are fetched with your system `git` binary. Private repositories
work through the credentials Git already has:

- SSH keys for `git@github.com:owner/repo.git`.
- HTTPS credential helpers.
- Local Git config and credential managers.

If `git clone` works in the same shell, `gpm` can use the same access.

## GitHub Release Sources

GitHub release sources use the GitHub API. Set `GITHUB_TOKEN` to authenticate:

```bash
export GITHUB_TOKEN=$(gh auth token)
gpm install
```

`GH_TOKEN` is also accepted as a fallback.

For classic tokens, private repositories generally need the `repo` scope. For
fine-grained tokens, grant contents read access to the repository.

GitHub returns `HTTP 404` for private repositories that the request cannot see.
If a private release exists but `gpm` reports a 404, check that a token is set
and that it has access to the repository.

## Archive Sources

Archive sources are fetched from direct HTTP(S) URLs. If the host requires
authentication, use the URL format supported by that host. `gpm` does not
provide an archive-specific token environment variable.
