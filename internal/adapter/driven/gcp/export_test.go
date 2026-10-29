package gcp

// WithPages makes the catalog read the pages of these families (for tests).
func WithPages(c *Catalog, pages []string) *Catalog {
	c.web.pages = pages
	return c
}
