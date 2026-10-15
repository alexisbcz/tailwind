package tailwind

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// --- candidate extraction --------------------------------------------------------

// Extract returns the class candidates found in content. Like Tailwind's own
// scanner it works on any text (Go, HTML, templates, Markdown); candidates
// that are not Tailwind classes are ignored by the compiler.
//
// The boundary rules follow Tailwind's scanner (oxide), established by
// probing the official CLI: a candidate must start the input or follow
// whitespace, a quote or '>', and must end the input or precede whitespace,
// a quote, '<', '\' or an unmatched ']'. So "fn(flex)", "flex." and "a=flex"
// yield nothing, while class="flex", 'flex' and >flex< do. Inside [...] and
// (...) only whitespace and double quotes end a candidate, so arbitrary values
// like "[&_svg:not([class*='size-'])]:size-4" stay whole.
func Extract(content []byte) []string {
	seen := make(map[string]struct{})
	var out []string
	n := len(content)
	for i := 0; i < n; {
		if isDelimiter(content[i]) {
			i++
			continue
		}
		// "['a', 'b']" is an array literal, not an arbitrary value.
		if content[i] == '[' && i+1 < n && (isSpace(content[i+1]) || content[i+1] == '\'' || content[i+1] == '"' || content[i+1] == '`') {
			i++
			continue
		}
		start, depth := i, 0
		for i < n {
			c := content[i]
			if isSpace(c) || c == '"' || c == '`' {
				break
			}
			if depth > 0 {
				switch c {
				case '[', '(':
					depth++
				case ']', ')':
					depth--
				}
				i++
				continue
			}
			if c == '[' || (c == '(' && i > start && (content[i-1] == '-' || content[i-1] == ':' || content[i-1] == '/')) {
				depth++
				i++
				continue
			}
			if isDelimiter(c) {
				break
			}
			i++
		}
		end := i
		// "p-4:" as an object key: the colon ends the candidate.
		after := byte(0)
		if end < n {
			after = content[end]
		}
		tok := content[start:end]
		if len(tok) > 1 && tok[len(tok)-1] == ':' && depth == 0 {
			tok, after = tok[:len(tok)-1], ':'
		}
		if !validBefore(content, start) || !validAfter(after, end == n) || len(tok) == 0 || len(tok) > 512 {
			continue
		}
		if _, ok := seen[string(tok)]; !ok {
			seen[string(tok)] = struct{}{}
			out = append(out, string(tok))
		}
	}
	return out
}

// ExtractVariables returns the CSS custom property names (--name) in content.
// Tailwind emits a theme variable when its name appears anywhere in the
// sources, as in var(--color-amber-50), so these need no boundary.
func ExtractVariables(content []byte) []string {
	var out []string
	for i := 0; i+2 < len(content); i++ {
		if content[i] != '-' || content[i+1] != '-' || (i > 0 && isNameChar(content[i-1])) {
			continue
		}
		j := i + 2
		for j < len(content) && isNameChar(content[j]) {
			j++
		}
		if j > i+2 {
			out = append(out, string(content[i:j]))
		}
		i = j
	}
	return out
}

// extractVarUsages returns the names used as var(--name) in a stylesheet.
// For .css sources the official scanner counts only these: declarations
// like "--x: 1" and theme(--x) do not mark a theme variable as used.
func extractVarUsages(css []byte) []string {
	var out []string
	for rest := css; ; {
		i := bytes.Index(rest, []byte("var(--"))
		if i < 0 {
			return out
		}
		rest = rest[i+4:]
		j := 2
		for j < len(rest) && isNameChar(rest[j]) {
			j++
		}
		if j > 2 {
			out = append(out, string(rest[:j]))
		}
		rest = rest[j:]
	}
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 0x80
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// isDelimiter reports characters that end a candidate outside brackets.
func isDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v', '"', '\'', '`', '<', '>', '{', '}', ';', ',', '=', '(', ')', ']', '\\', '|', '^', '$':
		return true
	}
	return false
}

func validBefore(content []byte, start int) bool {
	if start == 0 {
		return true
	}
	switch c := content[start-1]; c {
	case '"', '\'', '`', '>':
		return true
	default:
		return isSpace(c)
	}
}

func validAfter(c byte, atEnd bool) bool {
	if atEnd {
		return true
	}
	switch c {
	case '"', '\'', '`', '<', '\\', ']', ':':
		return true
	default:
		return isSpace(c)
	}
}

// --- file discovery --------------------------------------------------------------

// Directories never descended into unless a pattern names them. Other
// hidden directories are scanned, as by the official CLI.
var ignoredDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".jj": true, "node_modules": true,
}

// Files that cannot contain classes: other stylesheet languages, lock
// files and binaries. (.css files are scanned for CSS variables only.)
var ignoredExts = map[string]bool{
	".scss": true, ".sass": true, ".less": true, ".styl": true,
	".lock": true, ".lockb": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true,
	".ico": true, ".bmp": true, ".tif": true, ".tiff": true, ".psd": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".zip": true, ".gz": true, ".tgz": true, ".tar": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true, ".zst": true,
	".mp3": true, ".mp4": true, ".webm": true, ".mov": true, ".avi": true, ".wav": true, ".ogg": true, ".flac": true, ".m4a": true,
	".pdf": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".wasm": true, ".bin": true,
	".o": true, ".a": true, ".class": true, ".jar": true, ".db": true, ".sqlite": true, ".pyc": true,
}

var ignoredFiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lock": true, "bun.lockb": true,
	"Cargo.lock": true, "composer.lock": true, "Gemfile.lock": true,
}

const maxFileSize = 4 << 20

// sourceSet is the resolved list of include and exclude globs.
type sourceSet struct {
	includes []include
	excludes []*regexp.Regexp
}

type include struct {
	root    string         // directory to walk
	match   *regexp.Regexp // full slash path must match
	pattern string         // the pattern, for deciding which ignores apply
}

func (s *sourceSet) add(base, pattern string, negated bool) {
	full := pattern
	if !filepath.IsAbs(full) {
		full = filepath.Join(base, filepath.FromSlash(pattern))
	}
	full = filepath.ToSlash(filepath.Clean(full))
	if !hasMeta(full) {
		if st, err := os.Stat(filepath.FromSlash(full)); err == nil && st.IsDir() {
			full += "/**/*"
		}
	}
	re := globRegexp(full)
	if negated {
		s.excludes = append(s.excludes, re)
		return
	}
	s.includes = append(s.includes, include{root: filepath.FromSlash(staticPrefix(full)), match: re, pattern: full})
}

// files returns every file the sources select, deduplicated and sorted.
func (s *sourceSet) files() ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, inc := range s.includes {
		st, err := os.Stat(inc.root)
		if err != nil {
			continue // a pattern matching nothing is not an error
		}
		if !st.IsDir() {
			p := filepath.ToSlash(inc.root)
			if inc.match.MatchString(p) && !s.excluded(p) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			continue
		}
		ign := &gitignores{}
		err = filepath.WalkDir(inc.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, like Tailwind
			}
			sp := filepath.ToSlash(p)
			if d.IsDir() {
				if p != inc.root && ignoredDirs[d.Name()] && !strings.Contains(inc.pattern, "/"+d.Name()+"/") {
					return filepath.SkipDir
				}
				if p != inc.root && ign.ignored(sp, true) {
					return filepath.SkipDir
				}
				ign.load(sp)
				return nil
			}
			if !d.Type().IsRegular() || seen[sp] || !inc.match.MatchString(sp) || s.excluded(sp) {
				return nil
			}
			if ignoredFiles[d.Name()] || ignoredExts[strings.ToLower(filepath.Ext(d.Name()))] {
				return nil
			}
			if ign.ignored(sp, false) {
				return nil
			}
			seen[sp] = true
			out = append(out, sp)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *sourceSet) excluded(p string) bool {
	for _, re := range s.excludes {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// readCandidates extracts candidates from every file, skipping binaries.
func readCandidates(files []string) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil || len(b) > maxFileSize || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			continue
		}
		var found []string
		if strings.EqualFold(filepath.Ext(f), ".css") {
			found = extractVarUsages(b)
		} else {
			found = append(ExtractVariables(b), Extract(b)...)
		}
		for _, c := range found {
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				out = append(out, c)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// --- globs -------------------------------------------------------------------------

func hasMeta(p string) bool { return strings.ContainsAny(p, "*?[{") }

// staticPrefix returns the directory part of a glob before its first
// wildcard segment.
func staticPrefix(glob string) string {
	segs := strings.Split(glob, "/")
	for i, s := range segs {
		if hasMeta(s) {
			if i == 0 {
				return "/"
			}
			return strings.Join(segs[:i], "/")
		}
	}
	return glob
}

// globRegexp compiles a glob with *, **, ? and {a,b} alternatives.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	inBrace := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			// "**/" matches zero or more directories; a trailing "**"
			// matches everything below.
			if i+2 < len(glob) && glob[i+2] == '/' {
				b.WriteString("(?:.*/)?")
				i += 2
			} else {
				b.WriteString(".*")
				i++
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			inBrace++
			b.WriteString("(?:")
		case c == '}' && inBrace > 0:
			inBrace--
			b.WriteString(")")
		case c == ',' && inBrace > 0:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile("^" + regexp.QuoteMeta(glob) + "$")
	}
	return re
}

// --- .gitignore ----------------------------------------------------------------------

type gitignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// gitignores collects the rules of every .gitignore met while walking.
// Rules apply below the directory of their file; later rules win.
type gitignores struct {
	rules []gitignoreRule
}

func (g *gitignores) load(dir string) {
	data, err := os.ReadFile(filepath.FromSlash(dir + "/.gitignore"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := gitignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		line = strings.TrimPrefix(line, `\`)
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimSuffix(line, "/")
		}
		var glob string
		if strings.Contains(line, "/") {
			glob = dir + "/" + strings.TrimPrefix(line, "/") // anchored to this directory
		} else {
			glob = dir + "/**/" + line // matches at any depth
		}
		// A matching directory ignores everything inside it.
		r.re = regexp.MustCompile(strings.TrimSuffix(globRegexp(glob).String(), "$") + "(?:/.*)?$")
		g.rules = append(g.rules, r)
	}
}

func (g *gitignores) ignored(p string, isDir bool) bool {
	ignored := false
	for _, r := range g.rules {
		if !r.re.MatchString(p) {
			continue
		}
		if r.dirOnly && !isDir && !r.matchesParent(p) {
			continue
		}
		ignored = !r.negate
	}
	return ignored
}

// matchesParent reports whether a dir-only rule matches one of p's parent
// directories (rather than p itself, a file).
func (r gitignoreRule) matchesParent(p string) bool {
	for dir := filepath.ToSlash(filepath.Dir(p)); dir != "." && dir != "/" && dir != ""; dir = filepath.ToSlash(filepath.Dir(dir)) {
		if r.re.MatchString(dir) {
			return true
		}
		if parent := filepath.ToSlash(filepath.Dir(dir)); parent == dir {
			break
		}
	}
	return false
}
