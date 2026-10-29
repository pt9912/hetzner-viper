// Package live checks the real sources of viper find (the web pages of Hetzner and Google, the price
// lists of AWS and Azure, the rates of the European Central Bank): that what viper reads from them is
// still there. The checks have the build tag live and run every night in the workflow "price sources"
// (.github/workflows/sources.yml); locally: go test -tags live -count=1 -v ./internal/live/
//
// They check the form (enough types, the known ones among them, prices above zero), not the prices.
// A source that cannot be reached skips its check: that says nothing about viper.
package live
