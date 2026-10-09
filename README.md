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
  - key: XOPS            # empty repos list = all repositories in the project
    destination: XOPS    # optional Cloud project key; default = normalized source key
    repos: []
    overrides:           # optional per-repository re-mapping
      - repo: microservice-soev2
        destination: MICROSERVICE   # send this repo to a different Cloud project
      # - repo: legacy-api
      #   target_slug: api-v2       # and/or rename the Cloud repository slug
  - key: ~johnsmith      # maps to Cloud project JOHNSMITH by default
    repos: []

options:
  workers: 3
  on_error: continue          # continue | stop
  on_slug_collision: auto    # auto | fail; auto renames colliding slugs
  rollback: true              # delete a Cloud repo if this run created it and it failed
  create_cloud_projects: true
```

### Project mapping

By default each Server project maps to a Cloud project with the same normalized
key (`XOPS` → `XOPS`, `~johnsmith` → `JOHNSMITH`). Set `destination:` on a project
to send it somewhere else; multiple source projects may share one destination.

bimbucket always checks whether the destination project exists before migrating:

- exists → reused;
- missing and `create_cloud_projects: true` → created automatically;
- missing and `create_cloud_projects: false` → the affected repositories fail with
  `destination project "X" does not exist and create_cloud_projects is false`.

The TUI's **Projects** view shows the destination for each repository and whether
it already exists (`(ok)` / `(new)` / `(missing)`).

### Repository mapping

A project's `overrides:` list re-maps individual repositories. Each entry keys on
the source repo slug (`repo`) and may set:

- `destination:` — a Cloud project key for this repository only (default: the
  project's `destination`, or the normalized source key). Omit to inherit.
- `target_slug:` — a different Cloud repository slug (default: the normalized
  source slug). Omit to keep the source slug. The value is normalized like a
  source slug (lower-case; only `[a-z0-9._-]`).

```yaml
projects:
  - key: XOPS
    repos: []
    overrides:
      - repo: xopsapi              # stays in XOPS (no override needed)
      - repo: microservice-soev2
        destination: MICROSERVICE  # XOPS/microservice-soev2 -> MICROSERVICE/microservice-soev2
      - repo: legacy-api
        destination: MICROSERVICE
        target_slug: api-v2        # ...-> MICROSERVICE/api-v2
```

Overrides are validated at load time: `repo` must be non-empty and unique within
the project, `destination` must be a valid Cloud project key, and (when the
project lists `repos:`) the overridden repo must be one of them.

Cloud repository slugs are unique per workspace, so two source repositories with
the same slug (which Bitbucket Server allows, since it scopes slugs per project)
cannot both keep that slug on Cloud. The `on_slug_collision` option controls this:

- `auto` (default) — the colliding target slugs are prefixed with their source
  project key, e.g. `XOPS/api` → `xops-api` and `ABC/api` → `abc-api`.
- `fail` — the plan is rejected with a collision error.

An explicit `target_slug` override always wins; if it collides with another base
slug it is rejected so the plan stays unambiguous.

You can also re-map from the TUI: open the **Migrate** view, highlight a
repository, and press `e`. Edit the destination project and/or target slug and
press `enter`. Remapped rows are marked with `*` in the **Dest** column. The edit
is written straight back into `projects.yaml` (`overrides:`), preserving the
file's comments, so the YAML stays the single source of truth. Clearing both
fields removes the override.

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
