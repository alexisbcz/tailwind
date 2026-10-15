package tailwind

import "strings"

// Minify removes comments (keeping /*! license */ comments) and whitespace
// that cannot change meaning. It is deliberately conservative: strings,
// url(...) contents and whitespace that separates tokens (as in
// "calc(1px + 2px)" or "a :hover") are preserved.
//
// The official CLI minifies with Lightning CSS, which also rewrites values
// and adds vendor prefixes; that output is not reproduced here.
func Minify(css string) string {
	b := make([]byte, 0, len(css))
	pendingSpace := false
	last := byte(0) // last byte written

	// Space can go before and after these characters.
	tight := func(c byte) bool { return c == '{' || c == '}' || c == ';' || c == ',' || c == 0 }

	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return string(b) // unterminated comment runs to the end
			}
			if i+2 < len(css) && css[i+2] == '!' {
				if pendingSpace && !tight(last) {
					b = append(b, ' ')
				}
				b = append(b, css[i:i+2+end+2]...)
				b = append(b, '\n')
				last, pendingSpace = 0, false
			} else {
				pendingSpace = true
			}
			i += 2 + end + 1
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(css) && css[j] != c {
				if css[j] == '\\' {
					j++
				}
				j++
			}
			if pendingSpace && !tight(last) && last != ':' {
				b = append(b, ' ')
			}
			b = append(b, css[i:min(j+1, len(css))]...)
			last, pendingSpace = c, false
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			pendingSpace = true
		default:
			if c == '}' && last == ';' {
				b = b[:len(b)-1] // drop the semicolon before a closing brace
			}
			// Space after ':' only occurs in declarations and media features
			// ("color: red", "(width: 40rem)"); before it, it can be a
			// descendant combinator ("a :hover"), so that one stays.
			if pendingSpace && !tight(last) && !tight(c) && last != ':' {
				b = append(b, ' ')
			}
			b = append(b, c)
			last, pendingSpace = c, false
		}
	}
	return string(b)
}
