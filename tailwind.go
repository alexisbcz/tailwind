// Package tailwind generates Tailwind CSS v4 stylesheets with nothing but
// Go: no Node.js, no npm, no standalone binary.
//
// It runs Tailwind's own compiler (the tailwindcss npm package, vendored
// unmodified) in goja, a JavaScript engine written in Go, so the CSS
// generated for a class is exactly what Tailwind generates. Only the
// surroundings are Go: finding source files, extracting class candidates,
// and optional minification. Without minification the output matches the
// official CLI byte for byte (`tailwindcss -i in.css -o out.css`).
//
//	res, err := tailwind.Build(tailwind.Options{Input: "styles/app.css"})
//	os.WriteFile("static/app.css", []byte(res.CSS), 0o644)
//
// Most projects use the command instead, from a go:generate directive:
//
//	//go:generate go run github.com/alexisbcz/tailwind/cmd/tailwind -i styles/app.css -o static/app.css
//
// Supported: everything in Tailwind's CSS-first configuration (@import,
// @theme, @source, @utility, @variant, @custom-variant, @apply, source(...)),
// plus CSS-only packages found in node_modules. JavaScript plugins and
// configs (@plugin, @config) need Node.js and are rejected.
package tailwind

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Options configures Build.
type Options struct {
	// Input is the path of the entry stylesheet, typically containing
	// @import "tailwindcss". Relative @import and @source paths resolve
	// against its directory.
	Input string
	// CSS, if set, is used as the entry stylesheet instead of reading
	// Input. Paths then resolve against Input's directory, or Dir if Input
	// is empty.
	CSS string
	// Dir is the directory scanned by automatic source detection (when the
	// stylesheet doesn't use source(none) or source("...")), like the
	// working directory of the Tailwind CLI. Default: the current directory.
	Dir string
	// Output is the file the result will be written to. It is never
	// scanned, so a previous build cannot leak into the next one.
	Output string
	// Minify removes comments (except /*! ... */) and insignificant
	// whitespace.
	Minify bool
}

// Result is a generated stylesheet and what went into it.
type Result struct {
	CSS        string
	Files      []string // scanned source files
	Candidates []string // extracted candidates (most are not classes)
}

var engines = sync.Pool{}

func getEngine() (*engine, error) {
	if e, ok := engines.Get().(*engine); ok {
		return e, nil
	}
	return newEngine()
}

// Build compiles the stylesheet, scans the sources it declares and returns
// the generated CSS.
func Build(opts Options) (*Result, error) {
	dir := opts.Dir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	input, base, from := opts.CSS, dir, ""
	if opts.Input != "" {
		abs, err := filepath.Abs(opts.Input)
		if err != nil {
			return nil, err
		}
		base, from = filepath.Dir(abs), abs
		if input == "" {
			b, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			input = string(b)
		}
	}

	e, err := getEngine()
	if err != nil {
		return nil, err
	}
	// Tailwind's build() accumulates candidates across calls, so each Build
	// compiles afresh; only the runtime and the parsed library are reused.
	c, err := e.compileCSS(input, base, from)
	if err != nil {
		return nil, err
	}

	var set sourceSet
	switch {
	case c.root.auto:
		set.add(dir, "**/*", false)
	case !c.root.none:
		set.add(c.root.base, c.root.glob, false)
	}
	for _, s := range c.sources {
		set.add(s.Base, s.Pattern, s.Negated)
	}
	if opts.Output != "" {
		if abs, err := filepath.Abs(opts.Output); err == nil {
			set.add("/", filepath.ToSlash(abs), true)
		}
	}
	files, err := set.files()
	if err != nil {
		return nil, err
	}
	candidates, err := readCandidates(files)
	if err != nil {
		return nil, err
	}
	css, err := c.buildCSS(candidates)
	if err != nil {
		return nil, err
	}
	engines.Put(e)

	if opts.Minify {
		css = Minify(css)
	}
	return &Result{CSS: css, Files: files, Candidates: candidates}, nil
}

// Generate compiles css (resolving paths against base) and returns the
// stylesheet for exactly the given candidates, without scanning any files.
func Generate(css, base string, candidates []string) (string, error) {
	e, err := getEngine()
	if err != nil {
		return "", err
	}
	c, err := e.compileCSS(css, base, "")
	if err != nil {
		return "", err
	}
	out, err := c.buildCSS(candidates)
	if err != nil {
		return "", err
	}
	engines.Put(e)
	return out, nil
}

// WriteFile writes the result to path only if its content changed, so
// unchanged builds don't touch modification times.
func (r *Result) WriteFile(path string) (changed bool, err error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == r.CSS {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(r.CSS), 0o644); err != nil {
		return false, fmt.Errorf("tailwind: %w", err)
	}
	return true, nil
}
