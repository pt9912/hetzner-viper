#!/usr/bin/env bash

brew install go
brew install golangci-lint

# The browser tests of viper ui (e2e/, Playwright) need Node and the browsers with their system libraries (apt, so sudo). They are
# opt-in: set VIPER_E2E=1 in the settings of the machine that is made for this repository (VIPER_POST_CLONE runs this file).
# Playwright 1.65 knows Ubuntu 26.04, the system image of viper (an older one refuses --with-deps there).
if [ "${VIPER_E2E:-}" = 1 ]; then
  brew install node
  # a server has no GPU: WebKit (and Chromium) need the software GL of Mesa, which --with-deps does not bring
  sudo apt-get install -y --no-install-recommends libgl1-mesa-dri libegl1 libegl-mesa0 libgbm1 || echo "warning: Mesa not installed: WebKit may not start" >&2
  (cd e2e && npm ci && npx playwright install --with-deps chromium firefox webkit)
fi
