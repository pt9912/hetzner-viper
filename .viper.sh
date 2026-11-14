#!/usr/bin/env bash

brew install go
brew install golangci-lint

# The browser tests of viper ui (e2e/, Playwright) need Node and a browser with its system libraries (apt, so sudo). They are
# opt-in: set VIPER_E2E=1 in the settings of the machine that is made for this repository (VIPER_POST_CLONE runs this file).
if [ "${VIPER_E2E:-}" = 1 ]; then
  brew install node
  (cd e2e && npm ci && npx playwright install --with-deps chromium)
fi
