# Hetzner Viper

**English** | [Deutsch](README.de.md)

**VIPER** stands for **VI**rtual **PER**son: a code agent that works in your repository on a machine of its own, like one more person on the team.

## What is Hetzner Viper?

A single program, `viper`, that sets up a remote Linux machine on Hetzner Cloud with one command. On that machine the code agent [Claude Code](https://github.com/anthropics/claude-code) works remotely in your repository: it reads and changes code, runs builds and tests, and commits. There is one machine per Git repository, and the repository is cloned automatically during setup. You steer the agent via SSH, VS Code Remote-SSH or from an iPad, inside a tmux session that survives dropped connections. When you are done, you delete the machine again, and nothing keeps running or costing money.

It is meant for developers with their own Hetzner Cloud account (or an AWS account: `PROVIDER=aws` sets the machine up on Amazon EC2) and a Cloudflare account.

## What can I do today?

`viper` is one file (Linux, macOS) from the [Releases page](https://github.com/pt9912/hetzner-viper/releases), or with Homebrew: `brew install pt9912/viper/viper`. It needs the OpenSSH client (`ssh`, `ssh-keygen`, `ssh-keyscan`, `scp`) and nothing else: no Docker, `make`, `hcloud` or `jq` (`git` is used for the repository check and `code` to open VS Code, both optional).

```bash
mkdir ~/viper && cd ~/viper        # where your .env.local lives
viper setup                        # creates .env.local from a template; set DNS_DOMAIN and the tokens, then run it again:
viper setup                        # login key and local host CA
viper preflight web                # read-only checks: settings, Hetzner, Cloudflare, GitHub
viper up web --repo https://example.org/org/web.git   # set up machine "web", clone the repo
viper check web                    # read-only: DNS, access by name like another device, server state
viper ssh web                      # shell on the machine (--cmd "..." for one command)
viper ssh web --tmux                # attach to the tmux session "claude" (the one VS Code uses)
viper vscode web                   # open VS Code on the machine again (as viper up does)
viper down web                     # check for unsaved work, then delete machine "web"
viper dns web                      # print the DNS name of machine "web"
viper log [web] [--full]           # events of the machines, or one machine's full output
```

| Feature | Setting | What it does |
|---|---|---|
| Read-only check | `viper preflight <name>` | Checks everything before anything is created: settings, local setup, Hetzner token, server type available at the location, image, Cloudflare zone and record, GitHub token and repository access. `viper up` runs it first. |
| Several machines | `viper up api …` next to `web` | Each machine has its own server and DNS name `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (default prefix `claude`). |
| GitHub | `VIPER_GH_TOKEN` | A GitHub PAT, ideally fine-grained per machine with Contents and Pull requests read and write (a `GH_TOKEN` exported for your local `gh` is deliberately not used). The GitHub CLI `gh` is logged in on the server and `git` pushes over HTTPS, so the agent can also open pull requests. |
| Claude subscription | `VIPER_CLAUDE_TOKEN` | A long-lived token for your Claude subscription from `claude setup-token`. Claude Code is logged in on the server, so there is no `/login` after each new server. |
| Another model | `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN`, `VIPER_CLAUDE_MODEL` | Points Claude Code on the server at any Anthropic-compatible API, such as Ollama's cloud (`https://ollama.com`) or your own vLLM server. Preflight tests key and model with one small request. |
| Docker | always | Engine, Buildx and Compose come from Docker's own apt repository, so they are current. |
| Git author | `VIPER_GIT_USER_NAME`, `VIPER_GIT_USER_EMAIL` | The author of commits on the server; with `VIPER_GH_TOKEN` and no value it is taken from your GitHub account (name and noreply address). |
| code-server (optional) | `VIPER_CODE_SERVER=1`, `VIPER_CODE_SERVER_PASSWORD` | Installs `code-server`, VS Code in the browser, listening on `127.0.0.1` only. Reach it through an SSH tunnel (useful on an iPad). |
| Swap (optional) | `VIPER_SWAP` | Creates a swap file, as a factor of the server's RAM (`1` = as much as RAM, `0.5` = half) or a size (`4G`); off by default. It catches memory peaks (`docker build`, `npm install`) instead of the OOM killer. |
| Homebrew | `VIPER_HOMEBREW=0`, `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST`, `VIPER_BREW_PACKAGES`, `VIPER_BREWFILE=1` | `brew` is installed on every new server and available in all shells (`VIPER_HOMEBREW=0` skips it). Tools come from the three lists (tap, trust, install, in this order) or, with `VIPER_BREWFILE=1`, from a `Brewfile` in the repository. |
| Deleting safely | `viper down <name>` | Checks the Git state of all repositories and worktrees in the workspace, the home directory and the temp directories (uncommitted changes, commits on any branch that are on no remote, stash entries), flags entries at the top of the workspace that belong to no repository (files outside repositories elsewhere are not checked), lists ignored files, and additionally asks for `DISCARD` when there is unsaved work. |

The full guide with all tasks, settings and troubleshooting is the [user manual](docs/user/handbuch.md) (German). For a first real run, start with the section „Erster Testlauf mit einem kleinen Server“ there (server type `cpx02`, a few cents per hour).

## Why Hetzner Viper?

A code agent like Claude Code should run on a machine that is not your own laptop: powerful, isolated from your own data and credentials, reachable at any time through tmux, and usable from an iPad. Running such a machine permanently does not pay off. Setting up server, firewall, DNS, SSH hardening and repository by hand on every start is tedious and error-prone. `viper` turns this into one command to start and one to delete.

## Core idea

Machines are disposable: everything that matters lives in the Git repository. That is why there is deliberately no persistent volume, and `viper down` guards against the one risk this creates: unpushed work.

## What makes it trustworthy?

- **Tests:** `make check` (gofmt, vet, golangci-lint and more than 300 tests, all in Docker; CI on every push; `make help` lists the targets, `make build` builds `dist/viper` without a Go installation). They cover setup, readiness check, multiple machines, Git clone, firewall and DNS protection, the safety prompts of `viper down`, the interruption by Ctrl-C and the parser of `.env.local` (compared with real bash). The generated cloud-init user-data is compared with reference files and parsed as YAML.
- **Protection of foreign resources:** `viper` only changes and deletes DNS records it created itself.
- **Manual:** [`docs/user/handbuch.md`](docs/user/handbuch.md) (German) with version and change history.
- **Releases:** versions are tagged on the [Releases page](https://github.com/pt9912/hetzner-viper/releases) with the `viper` binaries and checksums; see [`docs/maintainer/releasing.md`](docs/maintainer/releasing.md) (German) for creating one.
- **License:** MIT, see [`LICENSE`](LICENSE).

## Security and limits

- SSH with public key only (no password, no root login). By default port 22 is open to the whole internet. Restrict `SSH_ALLOWED_CIDRS` if you only work from fixed networks.
- The user `builder` has passwordless sudo and Docker access, which equals root. Only use trusted repositories.
- The server uses a CA-signed SSH host certificate (valid for seven days, no automatic renewal). For this, the private host key is transferred in the cloud provider's user data (Hetzner user data, EC2 user data), where project administrators and processes on the server can read it. The CA key stays local. The computer that ran `viper up` pins the host key of each server and trusts only that key, so a key leaked from an earlier server is rejected and connections keep working after the certificate expires.
- The login key has no passphrase.
- **Everything on the server is lost when it is deleted**, including a Claude `/login` (with `VIPER_CLAUDE_TOKEN` every new server is logged in again automatically).
- The per-machine SSH config never forwards your ssh-agent or X11 to the server, even if your own `~/.ssh/config` does.
- `viper` creates paid resources. Never put API tokens into Git.
- Use a dedicated Hetzner project and try new settings with `viper preflight` and a small server first.

## Requirements

Linux or macOS, the `viper` binary and an OpenSSH client. VS Code Remote-SSH uses the local OpenSSH client; without VS Code use `viper ssh`. Plus a Hetzner Cloud API token (read and write), or an AWS login (`aws login`) with `PROVIDER=aws`, and a Cloudflare token with **Zone DNS Edit/Read** and the zone ID.

## License

MIT, see [`LICENSE`](LICENSE).
