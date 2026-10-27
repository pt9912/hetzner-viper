// Test-only exports of the unexported identifiers the external tests use.
package start

// BrewPkgScript exports brewPkgScript for the tests.
func BrewPkgScript() string { return brewPkgScript }

// CdLineScript exports cdLineScript for the tests.
func CdLineScript() string { return cdLineScript }

// CloneScript exports cloneScript for the tests.
func CloneScript() string { return cloneScript }

// GitIDScript exports gitIDScript for the tests.
func GitIDScript() string { return gitIDScript }

// InventoryScript exports inventoryScript for the tests.
func InventoryScript() string { return inventoryScript }

// Needle exports needle for the tests.
func Needle() string { return needle }

// Probe exports probe for the tests.
func Probe() string { return probe }

// SwapScript exports swapScript for the tests.
func SwapScript() string { return swapScript }

// JSONString exports jsonString for the tests.
func JSONString(s string) string { return jsonString(s) }
