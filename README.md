# Hetzner Viper

**English** | [Deutsch](README.de.md)

**VIPER** stands for **VI**rtual **PER**son: a code agent that works in your repository on a machine of its own, like one more person on the team.

## What is Hetzner Viper?

A set of Bash scripts that set up a remote Linux machine on Hetzner Cloud with a single command. On that machine the code agent [Claude Code](https://github.com/anthropics/claude-code) works remotely in your repository: it reads and changes code, runs builds and tests, and commits. There is one machine per Git repository, and the repository is cloned automatically during setup. You steer the agent via SSH, VS Code Remote-SSH or from an iPad, inside a tmux session that survives dropped connections. When you are done, you delete the machine again, and nothing keeps running or costing money.

It is meant for developers with their own Hetzner and Cloudflare accounts.

## What can I do today?

These commands exist. State of verification: offline tests against mocks (plus a real sshd), and runs on real Hetzner Cloud (cpx02, Ubuntu 26.04) with Cloudflare DNS: setup, preflight, creating, reusing and recreating a machine incl. the reboot after updates, check, ssh, delete incl. the unsaved-work protection, Claude via subscription token and via Ollama's cloud with tool use. **Not yet run for real:** `VIPER_GH_TOKEN`/gh, deploy keys, the iPad, a vLLM server (see [Limits](#security-and-limits)).

Everything runs in a Docker container via `make`, so locally you only need Docker and `make` (`ssh`, `hcloud`, `jq` etc. come from the [`Dockerfile`](Dockerfile)):

```bash
cp .env.local.example .env.local   # set DNS_DOMAIN and the tokens, then: chmod 600 .env.local
make                                                # list all targets (make help)
make setup                                          # create login key and local host CA
make preflight MACHINE=web                          # read-only checks: settings, Hetzner, Cloudflare, GitHub
make up MACHINE=web REPO=https://example.org/org/web.git   # set up machine "web", clone the repo
make check MACHINE=web                              # read-only: DNS, access by name like another device, server state
make ssh MACHINE=web                                # shell on the machine (CMD="..." for one command)
make down MACHINE=web                               # delete machine "web"
make dns MACHINE=web                                # print the DNS name of machine "web"
make log [MACHINE=web] [FULL=1]                     # events of the machines, or one machine's full output
```

Without the container, call the scripts directly: `./setup.sh`, `./preflight.sh web`, `./start.sh web <url>`, `./check.sh web`, `./connect.sh web`, `./stop.sh web`.

- `make preflight MACHINE=<name>` (or `./preflight.sh <name> [repo-url]`) checks everything read-only before anything is created: settings, local setup, Hetzner token, server type available at the location, image, Cloudflare zone and record, GitHub token and repository access. `start.sh` runs it first.
- `make test` runs the tests against mocks (offline, no cost); `make docker-test` runs the same tests in a container without network.
- Several machines in parallel: `make up MACHINE=api …` next to `web`. Each has its own server and DNS name `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (default prefix `claude`).
- With `VIPER_GH_TOKEN` (a GitHub PAT, ideally fine-grained per machine with Contents and Pull requests read and write; a `GH_TOKEN` exported for your local `gh` is deliberately not used) the GitHub CLI `gh` is logged in on the server and `git` pushes over HTTPS, so the agent can also open pull requests.
- With `VIPER_CLAUDE_TOKEN` (a long-lived token for your Claude subscription from `claude setup-token`) Claude Code is logged in on the server, so there is no `/login` after each new server.
- Or another model instead of Anthropic's: `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN` and `VIPER_CLAUDE_MODEL` point Claude Code on the server at any Anthropic-compatible API, such as Ollama's cloud (`https://ollama.com`) or your own vLLM server; preflight tests key and model with one small request.
- Docker (Engine, Buildx, Compose) comes from Docker's own apt repository, so it is current. Commits on the server get an author from `VIPER_GIT_USER_NAME` / `VIPER_GIT_USER_EMAIL`, or from your GitHub account (name and noreply address) when `VIPER_GH_TOKEN` is set.
- Optional: `VIPER_CODE_SERVER=1` (with `VIPER_CODE_SERVER_PASSWORD`) installs `code-server`, VS Code in the browser, listening on `127.0.0.1` only; reach it through an SSH tunnel (useful on an iPad).
- Optional: `VIPER_SWAP` creates a swap file, as a factor of the server's RAM (`1` = as much as RAM, `0.5` = half) or a size (`4G`); off by default. It catches memory peaks (`docker build`, `npm install`) instead of the OOM killer.
- Homebrew (`brew`) is installed on every new server and available in all shells; `VIPER_HOMEBREW=0` skips it. Tools come from `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST` and `VIPER_BREW_PACKAGES` (tap, trust, install, in this order) or, with `VIPER_BREWFILE=1`, a `Brewfile` in the repository.
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
- **Everything on the server is lost when it is deleted**, including a Claude `/login` (with `VIPER_CLAUDE_TOKEN` every new server is logged in again automatically).
- The per-machine SSH config never forwards your ssh-agent or X11 to the server, even if your own `~/.ssh/config` does.
- The scripts create paid resources. Never put API tokens into Git.
- Parts are not yet run against real services (see above). Use a dedicated Hetzner project and try new settings with `make preflight` and a small server first.

## Requirements

Only tested on Linux. Docker and `make` (recommended, also for macOS), **or** locally `bash`, `curl`, `jq`, `python3`, an OpenSSH client and the `hcloud` CLI. VS Code Remote-SSH needs a local OpenSSH client; without one use `make ssh`. Plus a Hetzner Cloud API token (read and write) and a Cloudflare token with **Zone DNS Edit/Read** and the zone ID.

## License

MIT, see [`LICENSE`](LICENSE).
