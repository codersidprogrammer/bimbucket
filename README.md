# bimbucket

**bimbucket** is a tool to help people migrate their repositories from a
self-hosted **Bitbucket Server / Data Center** (baseline **6.7**) to
**Bitbucket Cloud**.

It migrates repositories — branches, tags, commits, Git LFS objects, and the
default branch. Pull requests, permissions, and settings are intentionally out
of scope.

> The name is a small tribute: **bimbucket** was made for my daughter **bembi**.

## Features

- Interactive TUI with menu tabs: **Projects**, **Connection**, **Status**,
  **Config**, **History**, **Migrate**, and **About**.
- Select repositories with a searchable, scrollable table; toggle rows,
  select-all / clear-all on the visible set.
- Idempotent re-runs: a state ledger tracks what is already migrated.
- Safe by default: `-dry-run` mode, optional rollback of repositories created by
  a failed run, and a connection check before migrating.
- Ephemeral mirror clones (cleaned up automatically) — `git push --mirror` is
  never used, so Cloud-rejected `refs/pull-requests/*` are never pushed.
- Headless mode (`-no-tui`) for CI and automation, with JSONL + CSV reports.

## Requirements

- Go 1.24+ (to build from source) or a released Linux binary / `.deb` package.
- `git` on `PATH` (and `git-lfs` if you migrate LFS repositories).
- Network access to both your Server instance and `api.bitbucket.org`.

## Install

### Debian / Ubuntu (.deb)

Download the latest `.deb` from the
[Releases](https://github.com/codersidprogrammer/bimbucket/releases) page and:

```sh
sudo dpkg -i bimbucket_<version>_linux_amd64.deb
```

### Prebuilt binary

Download the `linux_amd64` (or `linux_arm64`) archive from Releases, then:

```sh
tar -xzf bimbucket_<version>_linux_amd64.tar.gz
sudo install -m 0755 bimbucket /usr/local/bin/bimbucket
```

### From source

```sh
go install github.com/codersidprogrammer/bimbucket/cmd/bimbucket@latest
```

## Configuration

bimbucket reads credentials from environment variables (a `.env` file next to
the working directory is loaded automatically) and migration scope from a YAML
file.

Copy `.env.example` to `.env` and fill it in:

```sh
BITBUCKET_SERVER_USER=admin
BITBUCKET_SERVER_TOKEN=            # or BITBUCKET_SERVER_PASSWORD
BITBUCKET_CLOUD_EMAIL=you@example.com
BITBUCKET_CLOUD_API_TOKEN=         # scoped API token, see below
BITBUCKET_CLOUD_WORKSPACE=your-workspace
```

The installed binary looks for `./.env` in the **current working directory**. To
use a `.env` elsewhere (e.g. `/etc/bimbucket/.env`), pass `-env`:

```sh
bimbucket -config /etc/bimbucket/projects.yaml -env /etc/bimbucket/.env
```

Resolution order, highest first: real shell environment variables → the `.env`
file (`-env` path, or `./.env`) → defaults. Existing environment variables are
never overridden.

> **Cloud API tokens need Bitbucket scopes.** App passwords are removed. Create a
> token at <https://id.atlassian.com/manage-profile/security/api-tokens> with the
> Bitbucket app and these scopes: `read:user:bitbucket`,
> `read:workspace:bitbucket`, `read:project:bitbucket`, `admin:project:bitbucket`,
> `read:repository:bitbucket`, `write:repository:bitbucket`,
> `admin:repository:bitbucket`, `delete:repository:bitbucket`. A token without
> Bitbucket scopes fails with `HTTP 401: API Token provided has no Bitbucket scopes`.

Example `configs/projects.yaml`:

```yaml
source:
  base_url: https://bitbucket.example.com

target:
  workspace: your-workspace

projects:
  - key: XOPS        # empty repos list = all repositories in the project
    repos: []
  - key: ~johnsmith
    repos: []

options:
  workers: 3
  on_error: continue          # continue | stop
  on_slug_collision: fail
  rollback: true              # delete a Cloud repo if this run created it and it failed
  create_cloud_projects: true
```

## Usage

Run the interactive TUI:

```sh
bimbucket -config configs/projects.yaml
```

Navigate tabs with `1`-`7` or `tab`. In **Projects**, press `/` to filter and
`r` to refresh. In **Migrate**, use `↑/↓` to scroll, `space` to toggle a row,
`a`/`n` to select/clear the visible rows, `/` to filter, and `enter` to confirm.

Headless (CI / automation):

```sh
bimbucket -config configs/projects.yaml -no-tui -dry-run
```

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-config` | `configs/projects.yaml` | Path to YAML config |
| `-env` | `./.env` if present | Path to a `.env` file with credentials |
| `-dry-run` | `false` | Plan and report without writing to Cloud |
| `-workers` | `0` (config) | Override worker count |
| `-temp-dir` | OS temp | Base directory for ephemeral clones |
| `-log-dir` | `logs` | Directory for run logs and reports |
| `-state` | `migration-state.json` | Resume / idempotency state file |
| `-no-tui` | `false` | Run non-interactively |
| `-version` | | Print version and exit |

## Development

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

## Credits

Created and maintained by **Mochammad Dimas Editiya** ([@codersidprogrammer](https://github.com/codersidprogrammer)).

Made with love for my daughter, **bembi** — hence the name **bimbucket**.
