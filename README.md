# Hetzner Viper

**English** | [Deutsch](README.de.md)

**VIPER** stands for **VI**rtual **PER**son: a code agent that works in your repository on a machine of its own, like one more person on the team.

## What is Hetzner Viper?

A single program, `viper`, that sets up a remote Linux machine at a cloud provider (Hetzner Cloud, AWS, Google Cloud or Azure), or a pod in a Kubernetes cluster, or a container on your own Docker host, with one command. On that machine the code agent [Claude Code](https://github.com/anthropics/claude-code) works remotely in your repository: it reads and changes code, runs builds and tests, and commits. There is one machine per Git repository, and the repository is cloned automatically during setup. You steer the agent via SSH, VS Code Remote-SSH or from an iPad, inside a tmux session that survives dropped connections. When you are done, you delete the machine again, and nothing keeps running or costing money.

It is meant for developers with their own cloud account or their own cluster: **Hetzner Cloud** (the default), **AWS** (`PROVIDER=aws`, Amazon EC2) a **Kubernetes cluster** (`PROVIDER=kubernetes`, tested in k3d) or the **Docker host of your own computer** (`PROVIDER=compose`, no cloud account and no cluster needed). Google Cloud and Azure (`PROVIDER=gcp`, `azure`) exist but are only tested against simulated interfaces; a run in a real account is not documented. The DNS name of the machines comes from a Cloudflare zone; without Cloudflare and without a domain (`DNS_PROVIDER=none`) a machine is reached by its address (a pod or a container is always reached through the API of its host).

## What can I do today?

`viper` is one file (Linux, macOS, and Windows as `windows_amd64.exe`, which is only partly tested: `viper` inside WSL with the Linux binary is more reliable) from the [Releases page](https://github.com/pt9912/hetzner-viper/releases), or with Homebrew: `brew install pt9912/viper/viper`. It needs the OpenSSH client (`ssh`, `ssh-keygen`, `ssh-keyscan`, `scp`) and nothing else: no `make`, `hcloud` or `jq` (`git` is used for the repository check and `code` to open VS Code, both optional). Docker is needed only to build the image for pods (`viper image build`).

```bash
mkdir ~/viper && cd ~/viper        # where your .env.local lives
viper find --cpu 4 --ram 16 --disk 80   # which server type fits, at which provider, for how much (read-only, no machine needed)
viper setup                        # creates .env.local from a template; set DNS_DOMAIN (or DNS_PROVIDER=none) and the credentials, then run it again:
viper setup                        # login key and local host CA
viper preflight web                # read-only checks: settings, cloud provider, Cloudflare, GitHub
viper up web --repo https://example.org/org/web.git   # set up machine "web", clone the repo
viper check web                    # read-only: DNS, access by name like another device, server state
viper ssh web                      # shell on the machine (--cmd "..." for one command)
viper ssh web --tmux                # attach to the tmux session "claude" (the one VS Code uses)
viper vscode web                   # open VS Code on the machine again (as viper up does)
viper down web                     # check for unsaved work, then delete machine "web"
viper dns web                      # print the DNS name of machine "web"
viper log [web] [--full]           # events of the machines, or one machine's full output
viper ui                           # machines, check, preflight/up/down, NATS and logs in the browser (127.0.0.1 only)
viper up --group dev               # all machines named in groups/dev.env, at the same time (also down, check, preflight, ssh --cmd)
viper nats send dev "Push, please" --to beta   # talk to a NATS group from this computer (also peers, listen, broadcast, request)
viper image build --k3d viper      # the image of the machines of a Kubernetes cluster (PROVIDER=kubernetes), into a k3d cluster
viper mcp-binary --arch arm64 > f  # the built-in MCP server for NATS groups, as a file
```

| Feature | Setting | What it does |
|---|---|---|
| Read-only check | `viper preflight <name>` | Checks everything before anything is created: settings, local setup, cloud login (a Hetzner token, an AWS login, ...), server type available at the location, image, Cloudflare zone and record, GitHub token and repository access. `viper up` runs it first. For a group: `viper preflight --group <group>`. |
| Find a server type | `viper find --cpu N --ram GB --disk GB` | Looks up the cheapest server types with at least that much at Hetzner, AWS, Azure and Google Cloud and shows them in **one table, on one basis**: in euro (US dollars at the rate of the European Central Bank), with the same VAT for all (`--vat`), sorted by price, the system disk included, with the settings to copy. No provider needs an account: the public price lists are read once a day and kept in `~/.cache/viper` (a Hetzner token or a Google project, if set, is used first and gives the exact prices of your project). The fee for the public IPv4 address is in the prices. Read-only. |
| Cloud provider | `PROVIDER=hetzner` (default), `aws`, `gcp`, `azure`, `kubernetes`, `compose` | Where the machines run. Credentials, server type, location and image have defaults per provider; Hetzner, AWS and Kubernetes (in k3d) are the tried ones, `gcp` and `azure` are only tested against simulated interfaces (a run in a real account is not documented). |
| Pods in a Kubernetes cluster | `PROVIDER=kubernetes`, `SERVER_LOCATION` (the context), `K8S_IMAGE`, `K8S_*` | A machine is a pod of a cluster you already have, with a volume for the home directory and one for Homebrew; it runs without root. The image is the published one of your `viper` version (`ghcr.io/pt9912/viper-machine`, amd64 and arm64) unless `K8S_IMAGE` names another; `viper image build [--k3d CLUSTER]` builds one with Docker from files built into `viper`. With `K3D_REGISTRY` (and `K3D_CLUSTER_CREATE=1`, `K3D_AGENTS`) `viper up` makes a k3d registry, and the cluster if it is missing, puts the image in it and lets the pods pull it from there; `viper down` takes the cluster it made with the last machine and asks about the registry. Docker on the machine is a privileged sidecar by default (`K8S_DOCKER`). Reached through the API of the cluster, so no address, no DNS and no firewall. Tested in k3d, not in EKS, GKE or AKS. |
| Containers on a Docker host | `PROVIDER=compose`, `SERVER_LOCATION` (the Docker context), `COMPOSE_*` | A machine is a container on the Docker host of your own computer, started with `docker compose` from a `compose.yaml` that `viper up` writes (the machines of a group share one project and one file, a machine without a group has its own); the image is the published one of your `viper` version unless `COMPOSE_IMAGE` names another, and `viper up` pulls it when the host lacks it; it has a volume for the home directory and one for Homebrew and runs without root. Docker on the machine is by default a privileged second container (`COMPOSE_DOCKER`). Reached with `docker exec`, so without address, DNS or firewall. NATS groups work (the group is a Docker network). Tested on a local Linux Docker host. |
| No Cloudflare (optional) | `DNS_PROVIDER=none` | No DNS record, no domain and no Cloudflare account: a machine is reached with `viper ssh <name>` or by its address (on AWS `viper up` also shows the name EC2 gives it). |
| Extra AWS tags (optional) | `AWS_TAGS`, `AWS_TAGS_SHARED` | Tags for the instance and its volume (`AWS_TAGS`) and for the key pair and security group (`AWS_TAGS_SHARED`), applied at creation, so tag policies (`aws:RequestTag`) allow the start. Preflight checks them. |
| Several machines | `viper up api …` next to `web` | Each machine has its own server and DNS name `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (default prefix `claude`). |
| Machine groups | `groups/<group>.env`, `viper up\|down\|check --group <group>`, `viper ssh --group <group> --cmd …` | The file names the machines (`GROUP_MACHINES=alpha,beta`) and the settings they share; one command starts, checks or deletes them all **at the same time**, with a `[machine]` prefix on every line and a table of results. A new NATS group gets its server first, the other machines follow while it is set up; `down` deletes the clients first and the server last. A machine may be named in several groups; for a single machine `--in-group <group>` says whose settings apply (`viper up web --in-group dev`). |
| NATS groups (optional) | `NATS_ENABLED`, `NATS_GROUP`, `NATS_PORT`, `NATS_OBJECTS`, `NATS_OBJECTS_MAX`, `NATS_OBJECTS_FILE_MAX`, `NATS_OBJECTS_TTL` | Machines of a group talk to each other over NATS on a private network: TLS, an NKey for each machine, and subjects in which only the machine itself may send (the sender of a message is the server's word). The first machine becomes the server, the others clients; `viper down` revokes the key of a client. With Hetzner, in a Kubernetes cluster (k3d) and with Docker Compose it has run for real; AWS, Google Cloud and Azure are only tested against simulated interfaces. |
| Talk to a NATS group (optional) | `viper nats peers\|topics\|listen\|send\|broadcast\|request\|fetch <group>`, `viper nats init <group>` | Reads and sends messages of a group from your computer, as one of its machines (`--file` takes a long text from a file) or, after `init`, as the operator with a key of your own (through an SSH tunnel). `listen --all` shows the direct messages between the others too: nothing in a group is private. With `NATS_OBJECTS=true` a message can carry an image, a PDF or a ZIP file (`--pdf`, `--image`, `--zip`; `viper nats fetch` gets it): the file lies in the object store of the NATS server of the group, which every machine of the group can read and also delete. |
| Browser interface (optional) | `viper ui [--port N] [--no-open] [--readonly]` | A small web server on `127.0.0.1` in the same program, no extra software: your machines and groups with their state, `viper check`, `preflight`/`up`/`down` with live output, questions and cancel, the NATS groups (who is there, listen, send text and files, fetch a file) and the logs and settings (values only of settings that are no credentials). English or German. Only your computer can reach it, with a key that works once; the output replaces the known credentials with `[hidden]`. |
| Claude Code talks to other machines (optional) | `NATS_MCP`, `NATS_MCP_PUSH_FROM`, `NATS_MCP_TOPICS`, `NATS_MCP_AUTOALLOW` | Installs the MCP server `viper-nats-mcp` (built into `viper`, checked by SHA-256) on each machine of a NATS group and registers it with Claude Code: tools to list machines, send, ask and answer (and, with `NATS_OBJECTS`, send and fetch files: the parameter `attachment` and the tool `fetch`). Nothing in the group is private, and text from other agents is untrusted input: read the warnings in the manual first. |
| GitHub | `VIPER_GH_TOKEN` | A GitHub PAT, ideally fine-grained per machine with Contents and Pull requests read and write (a `GH_TOKEN` exported for your local `gh` is deliberately not used). The GitHub CLI `gh` is logged in on the server and `git` pushes over HTTPS, so the agent can also open pull requests. |
| Claude subscription | `VIPER_CLAUDE_TOKEN` | A long-lived token for your Claude subscription from `claude setup-token`. Claude Code is logged in on the server, so there is no `/login` after each new server. |
| Another model | `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN`, `VIPER_CLAUDE_MODEL` | Points Claude Code on the server at any Anthropic-compatible API, such as Ollama's cloud (`https://ollama.com`) or your own vLLM server. Preflight tests key and model with one small request. |
| Docker | always | On a VM, Engine, Buildx and Compose come from Docker's own apt repository, so they are current. In a pod or a container the client is in the image and the daemon is a privileged sidecar (or an outside one, or none). |
| Git author | `VIPER_GIT_USER_NAME`, `VIPER_GIT_USER_EMAIL` | The author of commits on the server; with `VIPER_GH_TOKEN` and no value it is taken from your GitHub account (name and noreply address). |
| code-server (optional, VMs) | `VIPER_CODE_SERVER=1`, `VIPER_CODE_SERVER_PASSWORD` | Installs `code-server`, VS Code in the browser, listening on `127.0.0.1` only. Reach it through an SSH tunnel (useful on an iPad). |
| Swap (optional, VMs) | `VIPER_SWAP` | Creates a swap file, as a factor of the server's RAM (`1` = as much as RAM, `0.5` = half) or a size (`4G`); off by default. It catches memory peaks (`docker build`, `npm install`) instead of the OOM killer. |
| Homebrew | `VIPER_HOMEBREW=0`, `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST`, `VIPER_BREW_PACKAGES`, `VIPER_BREWFILE=1` | `brew` is installed on every new server and available in all shells (`VIPER_HOMEBREW=0` skips it). Tools come from the three lists (tap, trust, install, in this order) or, with `VIPER_BREWFILE=1`, from a `Brewfile` in the repository. |
| Post-clone commands | `VIPER_POST_CLONE`, `VIPER_POST_CLONE_ON_ERROR` | Shell commands run on the server in the cloned repository, after the clone and Homebrew (login shell as `builder`, stop at the first failure, run once, right after the clone; after a failure the next `viper up` tries again). `VIPER_POST_CLONE_ON_ERROR=retry` (default), `ignore` or `abort` decides what a failure does. Log: `~/.config/viper/post-clone.log`. |
| Deleting safely | `viper down <name>` | Checks the Git state of all repositories and worktrees in the workspace, the home directory and the temp directories (uncommitted changes, commits on any branch that are on no remote, stash entries), flags entries at the top of the workspace that belong to no repository (files outside repositories elsewhere are not checked), lists ignored files, and additionally asks for `DISCARD` when there is unsaved work. |

The full guide with all tasks, settings and troubleshooting is the [user manual](docs/user/handbuch.md) (German). For a first real run, start with the section „Erster Testlauf mit einem kleinen Server“ there (server type `cpx02`, a few cents per hour).

## Why Hetzner Viper?

A code agent like Claude Code should run on a machine that is not your own laptop: powerful, isolated from your own data and credentials, reachable at any time through tmux, and usable from an iPad. Running such a machine permanently does not pay off. Setting up server, firewall, DNS, SSH hardening and repository by hand on every start is tedious and error-prone. `viper` turns this into one command to start and one to delete.

## Core idea

Machines are disposable: everything that matters lives in the Git repository. That is why a VM deliberately has no persistent volume (a pod has volumes only so that it survives being replaced by its cluster), and `viper down` guards against the one risk this creates: unpushed work.

## What makes it trustworthy?

- **Tests:** `make check` (gofmt, vet, golangci-lint and more than 700 tests, in the devcontainer; CI on every push; `make help` lists the targets, `make build` builds `dist/viper`, `make dist-docker` builds the release files without a Go installation). They cover setup, readiness check, multiple machines, Git clone, firewall and DNS protection, the safety prompts of `viper down`, the interruption by Ctrl-C and the parser of `.env.local` (compared with real bash). The generated cloud-init user-data is compared with reference files and parsed as YAML.
- **Protection of foreign resources:** `viper` only changes and deletes DNS records it created itself.
- **Real runs:** the end-to-end tests (build tag `e2e`) set a machine up on a real AWS account and delete it again (`viper up` and `viper down`, x86 and Arm, without Cloudflare). NATS groups and `viper nats` have run on Hetzner, pods and NATS groups of pods in a k3d cluster, and the image of the pods has its own test (`make image-test`). See [`docs/maintainer/architektur.md`](docs/maintainer/architektur.md) (German) for the architecture and the tests.
- **Manual:** [`docs/user/handbuch.md`](docs/user/handbuch.md) (German) with version and change history.
- **Releases:** versions are tagged on the [Releases page](https://github.com/pt9912/hetzner-viper/releases) with the `viper` binaries and checksums; see [`docs/maintainer/releasing.md`](docs/maintainer/releasing.md) (German) for creating one. Working on `viper` (devcontainer, gates): [`docs/maintainer/developer.md`](docs/maintainer/developer.md) (German).
- **License:** MIT, see [`LICENSE`](LICENSE).

## Security and limits

- SSH with public key only (no password, no root login). By default port 22 is open to the whole internet. Restrict `SSH_ALLOWED_CIDRS` if you only work from fixed networks.
- On a VM the user `builder` has passwordless sudo and Docker access, which equals root. Only use trusted repositories. A pod runs as `builder` without root and without sudo; its Docker daemon, by default, is a privileged container, which is root on the node if it is broken into: use it in k3d or in a cluster of your own, not in a shared one.
- The server uses a CA-signed SSH host certificate (valid for seven days, no automatic renewal). For this, the private host key is transferred in the cloud provider's user data (Hetzner user data, EC2 user data), where project administrators and processes on the server can read it. The CA key stays local. The computer that ran `viper up` pins the host key of each server and trusts only that key, so a key leaked from an earlier server is rejected and connections keep working after the certificate expires.
- The login key has no passphrase.
- **Everything on the server is lost when it is deleted** (for a pod, with its volumes), including a Claude `/login` (with `VIPER_CLAUDE_TOKEN` every new server is logged in again automatically).
- The per-machine SSH config never forwards your ssh-agent or X11 to the server, even if your own `~/.ssh/config` does.
- `viper ui` listens on `127.0.0.1` only, answers only to that address, hands out a session cookie for a key that works once and replaces the known credentials in its output with `[hidden]`. It runs the same commands as the terminal, so it can create and delete servers: start it only on a computer you trust.
- `viper` creates paid resources. Never put API tokens into Git.
- Use a dedicated project or account at your cloud provider and try new settings with `viper preflight` and a small server first.

## Requirements

Linux, macOS or Windows (partly tested; WSL is more reliable, see the manual), the `viper` binary and an OpenSSH client. VS Code Remote-SSH uses the local OpenSSH client; without VS Code use `viper ssh`. Plus an account at a cloud provider (a Hetzner Cloud API token with read and write, or an AWS login with `aws login` and `PROVIDER=aws`; Google Cloud and Azure are described in the manual) or a Kubernetes cluster with a context in your `kubeconfig` (`PROVIDER=kubernetes`, Docker to build the image; k3d works) and, for the DNS name, a Cloudflare token with **Zone DNS Edit/Read** and the zone ID (not needed with `DNS_PROVIDER=none`).

## License

MIT, see [`LICENSE`](LICENSE).
