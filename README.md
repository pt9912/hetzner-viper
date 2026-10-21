# Hetzner Viper

**English** | [Deutsch](README.de.md)

**VIPER** stands for **VI**rtual **PER**son: a code agent that works in your repository on a machine of its own, like one more person on the team.

## What is Hetzner Viper?

A set of Bash scripts that set up a remote Linux machine on Hetzner Cloud with a single command. On that machine the code agent [Claude Code](https://github.com/anthropics/claude-code) works remotely in your repository: it reads and changes code, runs builds and tests, and commits. There is one machine per Git repository, and the repository is cloned automatically during setup. You steer the agent via SSH, VS Code Remote-SSH or from an iPad, inside a tmux session that survives dropped connections. When you are done, you delete the machine again, and nothing keeps running or costing money.

It is meant for developers with their own Hetzner and Cloudflare accounts.

## What can I do today?

These commands exist. State of verification: the scripts are tested against simulated Hetzner and Cloudflare APIs, **not** against real accounts (see [Limits](#security-and-limits)).

Everything runs in a Docker container via `make`, so locally you only need Docker and `make` (`ssh`, `hcloud`, `jq` etc. come from the [`Dockerfile`](Dockerfile)):

```bash
cp .env.local.example .env.local   # set DNS_DOMAIN and the tokens, then: chmod 600 .env.local
make setup                                          # create login key and local host CA
make preflight MACHINE=web                          # read-only checks: settings, Hetzner, Cloudflare, GitHub
make up MACHINE=web REPO=https://example.org/org/web.git   # set up machine "web", clone the repo
make ssh MACHINE=web                                # shell on the machine (CMD="..." for one command)
make down MACHINE=web                               # delete machine "web"
```

Without the container, call the scripts directly: `./setup.sh`, `./preflight.sh web`, `./start.sh web <url>`, `./connect.sh web`, `./stop.sh web`.

- `make preflight MACHINE=<name>` (or `./preflight.sh <name> [repo-url]`) checks everything read-only before anything is created: settings, local setup, Hetzner token, server type available at the location, image, Cloudflare zone and record, GitHub token and repository access. `start.sh` runs it first.
- `make test` runs the tests against mocks (offline, no cost); `make docker-test` runs the same tests in a container without network.
- Several machines in parallel: `make up MACHINE=api …` next to `web`. Each has its own server and DNS name `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (default prefix `claude`).
- With `VIPER_GH_TOKEN` (a GitHub PAT, ideally fine-grained per machine; a `GH_TOKEN` exported for your local `gh` is deliberately not used) the GitHub CLI `gh` is logged in on the server and `git` pushes over HTTPS, so the agent can also open pull requests.
- `stop.sh` checks the Git state of all repositories and worktrees in the workspace, the home directory and the temp directories (uncommitted changes, commits on any branch that are on no remote, stash entries), flags entries at the top of the workspace that belong to no repository (files outside repositories elsewhere are not checked), lists ignored files, and additionally asks for `DISCARD` when there is unsaved work.

The full guide with all tasks, settings and troubleshooting is the [user manual](docs/user/handbuch.md) (German). For a first real run, start with the section „Erster Testlauf mit einem kleinen Server“ there (server type `cpx02`, a few cents per hour).

## Why Hetzner Viper?

A code agent like Claude Code should run on a machine that is not your own laptop: powerful, isolated from your own data and credentials, reachable at any time through tmux, and usable from an iPad. Running such a machine permanently does not pay off. Setting up server, firewall, DNS, SSH hardening and repository by hand on every start is tedious and error-prone. The scripts turn this into one command to start and one to delete.

## Core idea

Machines are disposable: everything that matters lives in the Git repository. That is why there is deliberately no persistent volume, and `stop.sh` guards against the one risk this creates: unpushed work.

## What makes it trustworthy?

- **Tests:** `make test` (syntax check, `shellcheck` if installed, mock tests in [`tests/`](tests/)). They cover setup, readiness check, multiple machines, Git clone, firewall and DNS protection, and the safety prompts of `stop.sh`. The generated cloud-init configuration was checked once with `cloud-init schema` (cloud-init 26.2, Ubuntu 26.04 and 24.04), as were package availability, the `sshd` hardening and the `gh` flags on 26.04, and the `hcloud` calls against the help of CLI v1.70.1.
- **Protection of foreign resources:** `dns.sh` only changes and deletes DNS records it created itself.
- **Manual:** [`docs/user/handbuch.md`](docs/user/handbuch.md) (German) with version and change history.
- **License:** MIT, see [`LICENSE`](LICENSE).

## Security and limits

- SSH with public key only (no password, no root login). By default port 22 is open to the whole internet. Restrict `SSH_ALLOWED_CIDRS` if you only work from fixed networks.
- The user `builder` has passwordless sudo and Docker access, which equals root. Only use trusted repositories.
- The server uses a CA-signed SSH host certificate (valid for seven days, no automatic renewal). For this, the private host key is transferred in the Hetzner user data, where project administrators and processes on the server can read it. The CA key stays local. The machine that ran `start.sh` pins the host key of each server and trusts only that key, so a key leaked from an earlier server is rejected and connections keep working after the certificate expires.
- The login key has no passphrase.
- **Everything on the server is lost when it is deleted**, including the Claude login.
- The scripts create paid resources. Never put API tokens into Git.
- Not tested against a real Hetzner or Cloudflare account. Try them in a throwaway project first.

## Requirements

Only tested on Linux. Docker and `make` (recommended, also for macOS), **or** locally `bash`, `curl`, `jq`, `python3`, an OpenSSH client and the `hcloud` CLI. VS Code Remote-SSH needs a local OpenSSH client; without one use `make ssh`. Plus a Hetzner Cloud API token (read and write) and a Cloudflare token with **Zone DNS Edit/Read** and the zone ID.

## License

MIT, see [`LICENSE`](LICENSE).
