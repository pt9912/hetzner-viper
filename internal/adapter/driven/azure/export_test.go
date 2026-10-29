package azure

// CalculatorSpecs is the sizes from the data of the pricing calculator at addr, as the catalog reads them.
func CalculatorSpecs(c *Catalog, addr string) Specs { return c.calculatorSpecs(addr) }
