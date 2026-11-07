# tools/mk/pinsync.mk: the pinned versions of the toolchain agree at all their places (d-check, module
# pinsync; the places are listed in .d-check.yml). Needs Docker, nothing else; the image is pinned by
# its digest and runs without network, with the repository mounted read-only.

DCHECK_IMAGE  ?= ghcr.io/pt9912/d-check:v0.91.0
DCHECK_DIGEST ?= sha256:5d2f650d102c1d8224bb26930ec96d237506c07612fb8e0dbcdc42291702c57f

# a set DCHECK_DIGEST wins over the tag of DCHECK_IMAGE
ifeq ($(strip $(DCHECK_DIGEST)),)
DCHECK_REF := $(DCHECK_IMAGE)
else
DCHECK_REF := ghcr.io/pt9912/d-check@$(DCHECK_DIGEST)
endif

GATE_CHECKS := pinsync $(GATE_CHECKS)

.PHONY: pinsync
pinsync: ## Go and golangci-lint are pinned to the same version in go.mod, Dockerfile and .devcontainer/Dockerfile (d-check)
	docker run --rm --network none -v "$(CURDIR):/repo:ro" $(DCHECK_REF)
