package natsmcp

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// Clean makes text from another machine safe to show to the agent: valid UTF-8, no control
// characters (except new line and tab), no escape sequences, no characters that change the
// direction of the text, have no width or are invisible (also the Unicode tags that can hide a text). It does not make the text trustworthy.
func Clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = ansi.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == 0x2028 || r == 0x2029: // line and paragraph separator
			return '\n'
		case r == '\r' || unicode.IsControl(r) || invisible(r):
			return -1
		}
		return r
	}, s)
}

// invisible reports the characters that show nothing or change how a text looks: direction, no width, the
// other format characters (among them the Unicode tags that can hide a text), variation selectors, fillers.
func invisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, // direction
		r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF, r == 0xAD: // no width
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF, // variation selectors
		r == 0x034F, r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0: // invisible fillers
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// EscapeTags writes &, < and > as entities, so a text cannot close the <channel> tag it is
// delivered in or open another one.
func EscapeTags(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Safe is Clean followed by EscapeTags: what is shown to the agent.
func Safe(s string) string { return EscapeTags(Clean(s)) }
