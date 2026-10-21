# Everything runs in a container (ssh, hcloud, jq, ... come from the Dockerfile).
# `make` without a target shows the help; descriptions are the `## ` comments below.
.DEFAULT_GOAL := help
TEST_IMAGE ?= hetzner-claude-tests
RUN_IMAGE  ?= hetzner-claude-run

include tests/tests.mk

# passed to the recipe shell as environment, expanded there in double quotes (URLs with & ; ?)
export MACHINE REPO CMD

.PHONY: help build setup preflight up ssh down

help: ## show this help
	@echo 'Usage: make <target> [MACHINE=<name>] [REPO=<url>] [CMD="<command>"] [FORCE=1]'
	@echo
	@awk -F ':.*## ' '/^[a-z-]+:.*## / { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo
	@echo 'Example: make preflight MACHINE=web && make up MACHINE=web REPO=https://github.com/<owner>/<repo>.git'

build: ## build the container image (done automatically by the other targets)
	docker build --target run --build-arg USER_UID=$$(id -u) --build-arg USER_HOME="$$HOME" -t $(RUN_IMAGE) .

# Runs as the host user with the host's ~/.ssh and state directory mounted at the same
# paths, so keys, host CA and ssh config end up where the host's ssh and VS Code expect them.
define RUN
	@mkdir -p "$$HOME/.ssh" "$$HOME/.local/state/hetzner-claude"
	docker run --rm -i $$(test -t 0 && echo -t) --user $$(id -u):$$(id -g) -e HOME="$$HOME" \
	  -e HCLOUD_TOKEN -e CLOUDFLARE_API_TOKEN -e CLOUDFLARE_ZONE_ID -e VIPER_GH_TOKEN -e VIPER_GH_HOST \
	  -v "$$HOME/.ssh:$$HOME/.ssh" -v "$$HOME/.local/state/hetzner-claude:$$HOME/.local/state/hetzner-claude" \
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

ssh: build ## shell or one command on the machine (MACHINE, [CMD])
	$(call NEED_MACHINE,[CMD="<command>"])
	$(call RUN,./connect.sh "$$MACHINE" $${CMD:+"$$CMD"})

down: build ## check for unsaved work and delete the machine (MACHINE, [FORCE=1])
	$(call NEED_MACHINE,[FORCE=1])
	$(call RUN,./stop.sh "$$MACHINE" $(if $(FORCE),--force))
