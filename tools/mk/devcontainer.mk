# tools/mk/devcontainer.mk: the devcontainer of this repository (.devcontainer/, u-boot.yaml).
#
# u-boot makes the devcontainer (u-boot generate devcontainer); these targets only start and stop
# it. The binary u-boot on the host is operation here, not part of the gate: check needs only
# Docker and make. Without it a target ends with exit 2. The container runs in the sandbox profile
# with a nested Docker; its workspace is a clone of origin, not the working tree.

U_BOOT ?= u-boot

# devcontainer.json refers to ${localEnv:GIT_TOKEN} and ${localEnv:GH_TOKEN} of the host, and u-boot
# passes only variables that are set. dev-up and dev-shell take them from `gh auth token` when they
# are missing; a value that is set wins. Without gh, or without a login, they stay empty. The values
# go through the environment of the recipe, never the command line.
dev-up dev-shell: export GIT_TOKEN ?= $(shell gh auth token 2>/dev/null)
dev-up dev-shell: export GH_TOKEN ?= $(shell gh auth token 2>/dev/null)

define dev_require
@command -v $(U_BOOT) >/dev/null 2>&1 || { echo "$(1): '$(U_BOOT)' not found on PATH" >&2; exit 2; }
endef

.PHONY: dev-up dev-down dev-shell dev-remove

dev-up: ## start the devcontainer (RECREATE=1 makes it new, INSTANCE=<name> picks an instance)
	$(call dev_require,dev-up)
	$(U_BOOT) devcontainer up $(if $(RECREATE),--recreate) $(if $(INSTANCE),--instance $(INSTANCE))

dev-down: ## stop the devcontainer (it stays; INSTANCE=<name> picks an instance)
	$(call dev_require,dev-down)
	$(U_BOOT) devcontainer down $(if $(INSTANCE),--instance $(INSTANCE))

dev-shell: ## start the devcontainer and open a shell in it (INSTANCE=<name> picks an instance)
	$(call dev_require,dev-shell)
	$(U_BOOT) devcontainer shell $(if $(INSTANCE),--instance $(INSTANCE))

dev-remove: ## remove the devcontainer AND its volumes (workspace clone, nested Docker) without asking
	$(call dev_require,dev-remove)
	$(U_BOOT) devcontainer remove --volumes --yes $(if $(INSTANCE),--instance $(INSTANCE))
