package model

import "strings"

// Quote quotes a word for the remote shell: one argument, whatever it contains.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteAll quotes the words and joins them with spaces.
func QuoteAll(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = Quote(w)
	}
	return strings.Join(q, " ")
}
