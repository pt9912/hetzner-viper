# Everything runs in a container (ssh, hcloud, jq, ... come from the Dockerfile).
# `make` without a target shows the help; descriptions are the `## ` comments below.
.DEFAULT_GOAL := help
TEST_IMAGE ?= hetzner-claude-tests
RUN_IMAGE  ?= hetzner-claude-run

include tests/tests.mk

# passed to the recipe shell as environment, expanded there in double quotes (URLs with & ; ?).
# $(value ...): make must not expand a $ in them itself (CMD='echo $HOME' stays as typed).
override MACHINE := $(value MACHINE)
override REPO := $(value REPO)
override CMD := $(value CMD)
export MACHINE REPO CMD

.PHONY: help build setup preflight up check ssh down log

help: ## show this help
	@echo 'Usage: make <target> [MACHINE=<name>] [REPO=<url>] [CMD="<command>"] [FORCE=1] [FULL=1]'
	@echo
	@awk -F ':.*## ' '/^[a-z-]+:.*## / { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo
	@echo 'Example: make preflight MACHINE=web && make up MACHINE=web REPO=https://github.com/<owner>/<repo>.git'

build: ## build the container image (done automatically by the other targets)
	docker build --target run --build-arg USER_UID=$$(id -u) --build-arg USER_HOME="$$HOME" $(if $(BASE_IMAGE),--build-arg BASE_IMAGE=$(BASE_IMAGE)) -t $(RUN_IMAGE) .

# ~/.ssh/config may be a symlink into a dotfiles repo: mount its target too (at the same
# path), otherwise the scripts cannot add their Include line from inside the container.
SSH_CONFIG_TARGET := $(shell readlink -e "$$HOME/.ssh/config" 2>/dev/null)
SSH_CONFIG_MOUNT := $(if $(filter-out $(HOME)/.ssh/%,$(SSH_CONFIG_TARGET)),-v "$(SSH_CONFIG_TARGET):$(SSH_CONFIG_TARGET)")

# Runs as the host user with the host's ~/.ssh and state directory mounted at the same
# paths, so keys, host CA and ssh config end up where the host's ssh and VS Code expect them.
define RUN
	@mkdir -p "$$HOME/.ssh" "$$HOME/.local/state/hetzner-claude"
	docker run --rm -i $$(test -t 0 && echo -t) --user $$(id -u):$$(id -g) -e HOME="$$HOME" \
	  $$(test -e /etc/localtime && echo -v /etc/localtime:/etc/host-localtime:ro) -e TZ="$${TZ:-$$(test -e /etc/localtime && echo :/etc/host-localtime)}" \
	  -e HCLOUD_TOKEN -e CLOUDFLARE_API_TOKEN -e CLOUDFLARE_ZONE_ID -e VIPER_GH_TOKEN -e VIPER_GH_HOST -e VIPER_CLAUDE_TOKEN -e VIPER_ANTHROPIC_BASE_URL -e VIPER_ANTHROPIC_AUTH_TOKEN -e VIPER_CLAUDE_MODEL -e VIPER_CLAUDE_MAX_CONTEXT_TOKENS -e VIPER_HOMEBREW -e VIPER_BREW_TAPS -e VIPER_BREW_TRUST -e VIPER_BREW_PACKAGES -e VIPER_BREWFILE -e VIPER_SWAP -e VIPER_CODE_SERVER -e VIPER_CODE_SERVER_PASSWORD -e VIPER_GIT_USER_NAME -e VIPER_GIT_USER_EMAIL \
	  -v "$$HOME/.ssh:$$HOME/.ssh" -v "$$HOME/.local/state/hetzner-claude:$$HOME/.local/state/hetzner-claude" $(SSH_CONFIG_MOUNT) \
	  -v "$(CURDIR)":/repo:ro $(RUN_IMAGE) $(1)
endef

define NEED_MACHINE
	@test -n "$(MACHINE)" || { echo 'Usage: make $@ MACHINE=<name> $(1)' >&2; exit 1; }
endef

setup: build ## create login key and host CA (once)
	$(call RUN,./setup.sh)

preflight: build ## read-only checks, creates nothing (MACHINE, [REPO])
	$(call NEED_MACHINE,[REPO=<url>])
	$(call RUN,./preflight.sh "$$MACHINE" "$$REPO")

up: build ## create the machine and clone the repo (MACHINE, [REPO])
	$(call NEED_MACHINE,[REPO=<url>])
	$(call RUN,./start.sh "$$MACHINE" "$$REPO")

check: build ## read-only checks of a running machine: DNS, access by name, server state (MACHINE)
	$(call NEED_MACHINE,)
	$(call RUN,./check.sh "$$MACHINE")

ssh: build ## shell or one command on the machine (MACHINE, [CMD])
	$(call NEED_MACHINE,[CMD="<command>"])
	$(call RUN,./connect.sh "$$MACHINE" $${CMD:+"$$CMD"})

# runs on the host, no container and no tokens needed
# the prefix is read as the scripts read it (bash sources the files: comments, quotes)
log: ## show the log: events of all machines, or of MACHINE; FULL=1 its complete output
	@d="$$HOME/.local/state/hetzner-claude"; \
	if [ -z "$$MACHINE" ]; then tail -n 50 "$$d/events.log" 2>/dev/null || echo 'no events yet'; exit 0; fi; \
	p=$$(bash -c 'set -a; for f in ./.env.local "./machines/$$1.env.local"; do [ ! -f "$$f" ] || . "$$f"; done; printf %s "$$SERVER_PREFIX"' _ "$$MACHINE" 2>/dev/null); \
	s="$${p:-claude}-$$MACHINE"; \
	if [ "$(FULL)" = 1 ]; then cat "$$d/logs/$$s.log" 2>/dev/null || echo "no log for $$s"; \
	else awk -v s="$$s" '$$2 == s' "$$d/events.log" 2>/dev/null | grep . || echo "no events for $$s"; fi

down: build ## check for unsaved work and delete the machine (MACHINE, [FORCE=1])
	$(call NEED_MACHINE,[FORCE=1])
	$(call RUN,./stop.sh "$$MACHINE" $(if $(filter 1 yes true,$(FORCE)),--force))
