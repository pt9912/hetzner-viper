# tools/mk/tool-pruefung.mk: the gate that checks the tools the build needs.
#
# It checks the installed Go and golangci-lint (the devcontainer made by u-boot) against the pins of
# go.mod and the Dockerfile, and that the tools of build, release and tests are there. The script is
# tools/tool-pruefung.sh. check starts with it, so a toolchain that does not carry the pins ends
# red before lint and tests run on it.

GATE_CHECKS := tool-pruefung $(GATE_CHECKS)

.PHONY: tool-pruefung
tool-pruefung: ## the installed Go and golangci-lint carry the pinned versions
	bash tools/tool-pruefung.sh
