// Package config reads the settings the Bash scripts read (.env.local, machines/<name>.env.local)
// and derives names and paths the way common.sh does.
package config

import (
	"fmt"
	"strings"
)

// Source applies the assignments of a settings file to vars, like `set -a; source file` would.
// It understands what such files contain: NAME=value, export NAME=value, comments, 'single' and
// "double" quotes (also over several lines), backslash escapes, $NAME and ${NAME}, a leading ~.
// Anything else that bash would run or expand ($(...), `...`, ${NAME:-x}, pipes) is an error,
// not silently misread. As in the scripts (set -u), a variable that is not set is an error.
// Errors name the line, never its content: it may hold a token. lookup supplies the variables
// not set by the files.
func Source(vars map[string]string, src string, lookup func(string) (string, bool)) error {
	p := &parser{s: src, vars: vars, lookup: lookup, line: 1}
	if i := strings.IndexByte(src, '\r'); i >= 0 {
		p.line += strings.Count(src[:i], "\n")
		return p.errf("carriage return (Windows line ending): convert the file to Unix line ends")
	}
	return p.run()
}

type parser struct {
	s      string
	i      int
	line   int
	vars   map[string]string
	lookup func(string) (string, bool)
}

func (p *parser) errf(format string, a ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, a...))
}

func (p *parser) eof() bool { return p.i >= len(p.s) }

func isSpace(c byte) bool { return c == ' ' || c == '\t' }
func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

func (p *parser) run() error {
	for !p.eof() {
		c := p.s[p.i]
		switch {
		case c == '\n':
			p.line++
			p.i++
		case isSpace(c) || c == '\r' || c == ';':
			p.i++
		case c == '#':
			for !p.eof() && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			if err := p.statement(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *parser) name() string {
	start := p.i
	if p.eof() || !isNameStart(p.s[p.i]) {
		return ""
	}
	for !p.eof() && isNameChar(p.s[p.i]) {
		p.i++
	}
	return p.s[start:p.i]
}

func (p *parser) statement() error {
	name := p.name()
	if name == "" {
		return p.errf("not an assignment")
	}
	exported := false
	if name == "export" && !p.eof() && isSpace(p.s[p.i]) {
		for !p.eof() && isSpace(p.s[p.i]) {
			p.i++
		}
		if name = p.name(); name == "" {
			return p.errf("not an assignment")
		}
		exported = true
	}
	if p.eof() || p.s[p.i] != '=' {
		// `export NAME` only marks a variable for export, which sourcing here does anyway
		if exported && (p.eof() || p.s[p.i] == '\n' || p.s[p.i] == ';' || isSpace(p.s[p.i])) {
			return nil
		}
		return p.errf("not an assignment")
	}
	p.i++
	val, err := p.word()
	if err != nil {
		return err
	}
	p.vars[name] = val
	return nil
}

func (p *parser) value(name string) (string, error) {
	if v, ok := p.vars[name]; ok {
		return v, nil
	}
	if v, ok := p.lookup(name); ok {
		return v, nil
	}
	return "", p.errf("variable %s is not set", name)
}

// word reads the value of an assignment up to unquoted whitespace, ';' or a line end.
func (p *parser) word() (string, error) {
	var b strings.Builder
	start, afterColon := true, false
	for !p.eof() {
		c := p.s[p.i]
		colon := false
		switch {
		case c == '\n' || isSpace(c) || c == '\r' || c == ';':
			return b.String(), nil
		case c == '\'':
			end := strings.IndexByte(p.s[p.i+1:], '\'')
			if end < 0 {
				return "", p.errf("unterminated '")
			}
			lit := p.s[p.i+1 : p.i+1+end]
			b.WriteString(lit)
			p.line += strings.Count(lit, "\n")
			p.i += end + 2
		case c == '"':
			p.i++
			if err := p.double(&b, p.line); err != nil {
				return "", err
			}
		case c == '\\':
			if p.i+1 >= len(p.s) {
				b.WriteByte(c) // a backslash at the very end stays
				p.i++
				break
			}
			if p.s[p.i+1] == '\n' { // line continuation
				p.line++
			} else {
				b.WriteByte(p.s[p.i+1])
			}
			p.i += 2
		case c == '$':
			if err := p.dollar(&b, false); err != nil {
				return "", err
			}
		case c == '~' && (start || afterColon):
			next := byte('\n')
			if p.i+1 < len(p.s) {
				next = p.s[p.i+1]
			}
			switch {
			case next == '/' || next == '\n' || next == ';' || next == ':' || isSpace(next):
				home, err := p.value("HOME")
				if err != nil {
					return "", err
				}
				b.WriteString(home)
			case isNameChar(next) || next == '+' || next == '-':
				return "", p.errf("unsupported ~ form (only ~ and ~/...)")
			default:
				b.WriteByte(c)
			}
			p.i++
		case strings.IndexByte("`|&<>()", c) >= 0:
			return "", p.errf("unsupported shell syntax %q (quote the value)", string(c))
		default:
			b.WriteByte(c)
			p.i++
			colon = c == ':'
		}
		start, afterColon = false, colon
	}
	return b.String(), nil
}

func (p *parser) double(b *strings.Builder, startLine int) error {
	for !p.eof() {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return nil
		case '\\':
			if p.i+1 < len(p.s) {
				switch n := p.s[p.i+1]; n {
				case '"', '\\', '$', '`':
					b.WriteByte(n)
					p.i += 2
					continue
				case '\n':
					p.line++
					p.i += 2
					continue
				}
			}
			b.WriteByte(c)
			p.i++
		case '$':
			if err := p.dollar(b, true); err != nil {
				return err
			}
		case '`':
			return p.errf("unsupported shell syntax: command substitution")
		default:
			if c == '\n' {
				p.line++
			}
			b.WriteByte(c)
			p.i++
		}
	}
	p.line = startLine
	return p.errf("unterminated \"")
}

// dollar expands $NAME and ${NAME} at p.i.
func (p *parser) dollar(b *strings.Builder, inQuotes bool) error {
	p.i++ // $
	if p.eof() {
		b.WriteByte('$')
		return nil
	}
	switch c := p.s[p.i]; {
	case c == '{':
		end := strings.IndexByte(p.s[p.i:], '}')
		if end < 0 {
			return p.errf("unterminated ${")
		}
		name := p.s[p.i+1 : p.i+end]
		if name == "" || !isNameStart(name[0]) || strings.IndexFunc(name, func(r rune) bool { return r > 127 || !isNameChar(byte(r)) }) >= 0 {
			return p.errf("unsupported expansion ${...} (only ${NAME})")
		}
		v, err := p.value(name)
		if err != nil {
			return err
		}
		b.WriteString(v)
		p.i += end + 1
	case isNameStart(c):
		name := p.name()
		v, err := p.value(name)
		if err != nil {
			return err
		}
		b.WriteString(v)
	case c == '(':
		return p.errf("unsupported shell syntax: command substitution")
	case (c == '\'' || c == '"') && !inQuotes:
		return p.errf("unsupported shell syntax: $%c...%c quoting", c, c)
	case c == '$' || c == '?' || c == '!' || c == '@' || c == '*' || c == '#' || c == '-' || (c >= '0' && c <= '9'):
		return p.errf("unsupported expansion $%c", c)
	default:
		b.WriteByte('$') // a lone $ is literal
	}
	return nil
}
