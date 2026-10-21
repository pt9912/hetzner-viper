# make test | docker-test            offline tests (see tests/tests.mk)
# make preflight MACHINE=web [REPO=<url>]  read-only checks (nothing is created)
# make up MACHINE=web [REPO=<url>]   real run in a container: create machine
# make down MACHINE=web [FORCE=1]    real run in a container: delete machine
TEST_IMAGE ?= hetzner-claude-tests
RUN_IMAGE  ?= hetzner-claude-run

include tests/tests.mk

# passed to the recipe shell as environment, expanded there in double quotes (URLs with & ; ?)
export MACHINE REPO

.PHONY: build setup preflight up down

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

setpreflight: build
	@test -n "$(MACHINE)" || { echo 'Usage: make preflight MACHINE=<name> [REPO=<url>]' >&2; exit 1; }
	$(call RUN,./preflight.sh "$$MACHINE" "$$REPO")

up: build
	$(call RUN,./setup.sh)

preflight: build
	@test -n "$(MACHINE)" || { echo 'Usage: make preflight MACHINE=<name> [REPO=<url>]' >&2; exit 1; }
	$(call RUN,./preflight.sh "$$MACHINE" "$$REPO")

up: build
	@test -n "$(MACHINE)" || { echo 'Usage: make up MACHINE=<name> [REPO=<url>]' >&2; exit 1; }
	$(call RUN,./start.sh "$$MACHINE" "$$REPO")

down: build
	@test -n "$(MACHINE)" || { echo 'Usage: make down MACHINE=<name> [FORCE=1]' >&2; exit 1; }
	$(call RUN,./stop.sh "$$MACHINE" $(if $(FORCE),--force))
