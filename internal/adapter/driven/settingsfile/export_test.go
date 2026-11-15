package settingsfile

// HelpNames are the settings that have a help text.
func HelpNames() []string {
	var out []string
	for n := range help {
		out = append(out, n)
	}
	return out
}
