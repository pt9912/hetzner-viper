# Everything runs in a container (ssh, hcloud, jq, ... come from the Dockerfile):
# make setup                                     login key and host CA (once)
# make preflight MACHINE=web [REPO=<url>]        read-only checks (nothing is created)
# make up MACHINE=web [REPO=<url>]               create the machine, clone the repo
# make ssh MACHINE=web [CMD='<command>']         shell (or one command) on the machine
# make down MACHINE=web [FORCE=1]                delete the machine
# make test | docker-test                        offline tests (see tests/tests.mk)
TEST_IMAGE ?= hetzner-claude-tests
RUN_IMAGE  ?= hetzner-claude-run

include tests/tests.mk

# passed to the recipe shell as environment, expanded there in double quotes (URLs with & ; ?)
export MACHINE REPO CMD

.PHONY: build setup preflight up ssh down

build:
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

setup: build
	$(call RUN,./setup.sh)

preflight: build
	$(call NEED_MACHINE,[REPO=<url>])
	$(call RUN,./preflight.sh "$$MACHINE" "$$REPO")

up: build
	$(call NEED_MACHINE,[REPO=<url>])
	$(call RUN,./start.sh "$$MACHINE" "$$REPO")

ssh: build
	$(call NEED_MACHINE,[CMD="<command>"])
	$(call RUN,./connect.sh "$$MACHINE" $${CMD:+"$$CMD"})

down: build
	$(call NEED_MACHINE,[FORCE=1])
	$(call RUN,./stop.sh "$$MACHINE" $(if $(FORCE),--force))
