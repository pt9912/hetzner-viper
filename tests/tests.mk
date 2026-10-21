# Test targets, included by the top-level Makefile.
TEST_SCRIPTS = common.sh dns.sh setup.sh start.sh stop.sh tests/run.sh tests/cases/*.sh tests/mocks/ssh tests/mocks/scp tests/mocks/code

.PHONY: test lint docker-test

# Offline tests against fake hcloud/curl/ssh (no network, no cost)
test: lint
	tests/run.sh

lint:
	@for f in $(TEST_SCRIPTS); do bash -n $$f || exit 1; done
	@if command -v shellcheck >/dev/null; then shellcheck -S error $(TEST_SCRIPTS); else echo 'shellcheck not installed, skipped'; fi

# Same tests in a clean container without network access
docker-test:
	docker build --target test -t $(TEST_IMAGE) .
	docker run --rm --network none -v "$(CURDIR)":/repo:ro $(TEST_IMAGE)
