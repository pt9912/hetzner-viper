# tools/mk/tool-pruefung.mk: the gate that checks the tools the build needs.
#
# With Go and golangci-lint on the PATH (the devcontainer made by u-boot, or a host with Go) it
# checks the installed tools against the pins of go.mod and the Dockerfile; without them it builds
# the images of the Dockerfile and checks those (a host with only Docker and make). The script is
# tools/tool-pruefung.sh. check starts with it, so a toolchain that does not carry the pins ends
# red before lint and tests run on it.

GATE_CHECKS := tool-pruefung $(GATE_CHECKS)

.PHONY: tool-pruefung
tool-pruefung: $(IMAGES) ## the tools of the build carry the pinned Go and golangci-lint (installed, or in the images)
	bash tools/tool-pruefung.sh $(if $(TOOLS_LOCAL),local,images $(DEV) viper-lint)
