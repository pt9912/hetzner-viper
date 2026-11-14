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

// Probe exports the probe of a VM for the tests.
func Probe() string { return vmProbe }

// NatsCertScript exports natsCertScript for the tests.
func NatsCertScript() string { return natsCertScript }

// SwapScript exports swapScript for the tests.
func SwapScript() string { return swapScript }

// JSONString exports jsonString for the tests.
func JSONString(s string) string { return jsonString(s) }

// NatsMCPInstallScript exports natsMCPInstallScript for the tests.
func NatsMCPInstallScript() string { return natsMCPInstallScript }

// NatsMCPClaudeScript exports natsMCPClaudeScript for the tests.
func NatsMCPClaudeScript() string { return natsMCPClaudeScript }

// NatsMCPTestScript exports natsMCPTestScript for the tests.
func NatsMCPTestScript() string { return natsMCPTestScript }

// CheckLinuxBinary exports checkLinuxBinary for the tests.
func CheckLinuxBinary(data []byte, goarch string) error { return checkLinuxBinary(data, goarch) }

// ContainerProbe exports the probe of a pod for the tests.
func ContainerProbe() string { return containerProbe }

// NatsCheckScript exports natsCheckScript for the tests.
func NatsCheckScript() string { return natsCheckScript }

// NatsDiskScript exports natsDiskScript for the tests.
func NatsDiskScript() string { return natsDiskScript }
