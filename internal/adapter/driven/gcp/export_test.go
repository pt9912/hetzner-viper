package gcp

import "google.golang.org/api/compute/v1"

// WithPages makes the catalog read the pages of these families (for tests).
func WithPages(c *Catalog, pages []string) *Catalog {
	c.web.pages = pages
	return c
}

// RuleProblems lets the tests reach ruleProblems.
func RuleProblems(r *compute.Firewall) []string { return ruleProblems(r) }
