package tailwind

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

// Version is the vendored Tailwind CSS version.
const Version = "4.3.0"

// dist holds Tailwind's compiler (dist/lib.js) and its stylesheets, copied
// unmodified from the tailwindcss npm package, plus the CSS-only packages
// shadcn/ui imports (see packages). All are MIT licensed.
//
//go:embed dist/lib.js dist/index.css dist/theme.css dist/preflight.css dist/utilities.css dist/pkg
var dist embed.FS

// packages maps CSS-only npm imports to embedded files, so they work
// without node_modules: tw-animate-css 1.4.0 and shadcn's tailwind.css
// (custom variants and keyframes used by the components).
var packages = map[string]string{
	"tw-animate-css":        "pkg/tw-animate-css/tw-animate.css",
	"tw-animate-css/prefix": "pkg/tw-animate-css/tw-animate-prefix.css",
	"shadcn/tailwind.css":   "pkg/shadcn/tailwind.css",
	"shadcn/tailwind":       "pkg/shadcn/tailwind.css",
}

// embeddedBase is the base path reported for stylesheets that come from the
// embedded package, so their relative imports ("./theme.css") resolve back to
// it. It can never collide with a real absolute path.
const embeddedBase = "tailwindcss:"

var (
	programOnce sync.Once
	program     *goja.Program
	programErr  error
)

// compiledLib parses lib.js once per process; each engine reuses it.
func compiledLib() (*goja.Program, error) {
	programOnce.Do(func() {
		src, err := dist.ReadFile("dist/lib.js")
		if err != nil {
			programErr = err
			return
		}
		// lib.js is a CommonJS bundle without require() calls.
		program, programErr = goja.Compile("tailwindcss/dist/lib.js",
			"(function(module, exports){"+string(src)+"\n})", true)
	})
	return program, programErr
}

// prelude supplies the few web-platform globals Tailwind's compiler uses
// that goja lacks: structuredClone (for cloning its CSS AST, which holds
// only plain objects, arrays, Maps and Sets), console.warn (warnings go to
// the host) and Intl.ListFormat (only used to word one error message).
//
// It works around two goja bugs that change Tailwind's rule order:
//
//   - Number(bigint) keeps only the low 64 bits (Number(1n << 70n) is 0).
//     Tailwind keeps each utility's variants as a BigInt bitmask and sorts
//     with Number(a - b), so masks past bit 63 sorted wrongly. Number is
//     wrapped to convert BigInts through their decimal string, which
//     parseFloat rounds exactly as the spec requires.
//
//   - Array.prototype.sort treats a comparator result of -0 as "less than"
//     (the spec says equal). It is replaced by the spec algorithm with a
//     stable merge sort, which orders exactly like V8 for any consistent
//     comparator.
const prelude = `(function (g, warn) {
  const NativeNumber = g.Number
  const FixedNumber = function Number(v) {
    const x = arguments.length === 0 ? 0 : typeof v === "bigint" ? parseFloat(v.toString()) : NativeNumber(v)
    return new.target ? new NativeNumber(x) : x
  }
  for (const k of Object.getOwnPropertyNames(NativeNumber)) {
    if (k !== "length" && k !== "name" && k !== "prototype") Object.defineProperty(FixedNumber, k, Object.getOwnPropertyDescriptor(NativeNumber, k))
  }
  Object.defineProperty(FixedNumber, "prototype", { value: NativeNumber.prototype })
  Object.defineProperty(NativeNumber.prototype, "constructor", { value: FixedNumber, writable: true, configurable: true, enumerable: false })
  g.Number = FixedNumber

  Object.defineProperty(Array.prototype, "sort", {
    writable: true, configurable: true, enumerable: false,
    value: function sort(cmp) {
      if (cmp !== undefined && typeof cmp !== "function") throw new TypeError("The comparison function must be either a function or undefined")
      const o = Object(this)
      const len = Math.min(Math.max(Math.trunc(Number(o.length)) || 0, 0), Number.MAX_SAFE_INTEGER)
      let vals = [], undef = 0
      for (let i = 0; i < len; i++) {
        if (i in o) { const v = o[i]; if (v === undefined) undef++; else vals.push(v) }
      }
      const c = cmp
        ? (a, b) => { const r = +cmp(a, b); return r !== r ? 0 : r }
        : (a, b) => { const x = String(a), y = String(b); return x < y ? -1 : x > y ? 1 : 0 }
      let buf = new Array(vals.length)
      for (let width = 1; width < vals.length; width *= 2) {
        for (let lo = 0; lo < vals.length; lo += 2 * width) {
          const mid = Math.min(lo + width, vals.length), hi = Math.min(lo + 2 * width, vals.length)
          let i = lo, j = mid, k = lo
          while (i < mid && j < hi) buf[k++] = c(vals[j], vals[i]) < 0 ? vals[j++] : vals[i++]
          while (i < mid) buf[k++] = vals[i++]
          while (j < hi) buf[k++] = vals[j++]
        }
        const t = vals; vals = buf; buf = t
      }
      let i = 0
      for (; i < vals.length; i++) o[i] = vals[i]
      for (let k = 0; k < undef; k++) o[i++] = undefined
      for (; i < len; i++) delete o[i]
      return o
    },
  })
  g.structuredClone = function clone(v, seen) {
    if (v === null || typeof v !== "object") return v
    seen = seen || new Map()
    if (seen.has(v)) return seen.get(v)
    let out
    if (v instanceof Date) return new Date(v.getTime())
    if (v instanceof RegExp) return new RegExp(v.source, v.flags)
    if (v instanceof Map) {
      out = new Map(); seen.set(v, out)
      v.forEach((val, k) => out.set(clone(k, seen), clone(val, seen)))
      return out
    }
    if (v instanceof Set) {
      out = new Set(); seen.set(v, out)
      v.forEach((val) => out.add(clone(val, seen)))
      return out
    }
    out = Array.isArray(v) ? new Array(v.length) : {}
    seen.set(v, out)
    for (const k of Object.keys(v)) out[k] = clone(v[k], seen)
    return out
  }
  const fmt = (args) => args.map(String).join(" ")
  g.console = { warn: (...a) => warn(fmt(a)), error: (...a) => warn(fmt(a)), log: (...a) => warn(fmt(a)) }
  g.Intl = g.Intl || {}
  g.Intl.ListFormat = g.Intl.ListFormat || class {
    constructor(_, o) { this.word = o && o.type === "disjunction" ? "or" : "and" }
    format(list) {
      const l = Array.from(list)
      if (l.length < 3) return l.join(" " + this.word + " ")
      return l.slice(0, -1).join(", ") + ", " + this.word + " " + l[l.length - 1]
    }
  }
})`

// Warn receives the compiler's warnings. The default writes them to stderr.
var Warn = func(msg string) { fmt.Fprintln(os.Stderr, "tailwind:", msg) }

// glue adapts Tailwind's promise-based API to synchronous Go calls. Go
// callbacks resolve stylesheets; results are delivered through done/fail.
const glue = `(function (tw, load) {
  return function (input, base, from, done, fail) {
    tw.compile(input, {
      base: base,
      from: from || undefined,
      loadStylesheet: async (id, base) => load(id, base),
      loadModule: async (id) => { throw new Error("JavaScript plugins and configs are not supported (" + id + "); use CSS-only plugins") },
    }).then(function (c) {
      done({
        sources: c.sources,
        root: c.root === null ? null : (c.root === "none" ? "none" : c.root),
        build: function (candidates) { return c.build(candidates) },
      })
    }, function (e) { fail(String(e && e.message || e)) })
  }
})`

// engine is one JavaScript runtime with Tailwind loaded. goja runtimes are
// not safe for concurrent use, so each engine is used by one goroutine at a
// time.
type engine struct {
	vm      *goja.Runtime
	compile goja.Callable
}

func newEngine() (*engine, error) {
	prog, err := compiledLib()
	if err != nil {
		return nil, fmt.Errorf("tailwind: loading compiler: %w", err)
	}
	vm := goja.New()
	pre, err := vm.RunString(prelude)
	if err != nil {
		return nil, err
	}
	preFn, _ := goja.AssertFunction(pre)
	if _, err := preFn(goja.Undefined(), vm.GlobalObject(), vm.ToValue(func(msg string) { Warn(msg) })); err != nil {
		return nil, err
	}
	wrapper, err := vm.RunProgram(prog)
	if err != nil {
		return nil, fmt.Errorf("tailwind: loading compiler: %w", err)
	}
	module := vm.NewObject()
	exports := vm.NewObject()
	_ = module.Set("exports", exports)
	init, _ := goja.AssertFunction(wrapper)
	if _, err := init(goja.Undefined(), module, exports); err != nil {
		return nil, fmt.Errorf("tailwind: loading compiler: %w", err)
	}

	glueFn, err := vm.RunString(glue)
	if err != nil {
		return nil, err
	}
	makeCompile, _ := goja.AssertFunction(glueFn)
	loader := vm.ToValue(func(id, base string) (map[string]string, error) {
		p, b, content, err := loadStylesheet(id, base)
		if err != nil {
			return nil, err
		}
		return map[string]string{"path": p, "base": b, "content": content}, nil
	})
	compileFn, err := makeCompile(goja.Undefined(), module.Get("exports"), loader)
	if err != nil {
		return nil, err
	}
	compile, _ := goja.AssertFunction(compileFn)
	return &engine{vm: vm, compile: compile}, nil
}

// compiled is the result of compiling an input stylesheet.
type compiled struct {
	eng     *engine
	sources []Source
	root    rootSpec
	build   goja.Callable
}

// rootSpec mirrors the "root" Tailwind reports for source(...) on @import:
// auto (scan the working directory), none, or an explicit directory.
type rootSpec struct {
	auto bool
	none bool
	base string
	glob string
}

// Source is an @source directive, resolved against the directory of the
// stylesheet that declared it.
type Source struct {
	Base    string
	Pattern string
	Negated bool
}

func (e *engine) compileCSS(input, base, from string) (*compiled, error) {
	var (
		result   *goja.Object
		failure  string
		finished bool
	)
	done := func(o *goja.Object) { result, finished = o, true }
	fail := func(msg string) { failure, finished = msg, true }
	// The promise chain runs on goja's job queue, which drains before the
	// call returns.
	if _, err := e.compile(goja.Undefined(), e.vm.ToValue(input), e.vm.ToValue(base), e.vm.ToValue(from), e.vm.ToValue(done), e.vm.ToValue(fail)); err != nil {
		return nil, err
	}
	if !finished {
		return nil, errors.New("tailwind: compile did not complete")
	}
	if failure != "" {
		return nil, errors.New("tailwind: " + failure)
	}

	c := &compiled{eng: e}
	build, ok := goja.AssertFunction(result.Get("build"))
	if !ok {
		return nil, errors.New("tailwind: compiler returned no build function")
	}
	c.build = build

	switch r := result.Get("root").Export().(type) {
	case nil:
		c.root.auto = true
	case string: // "none"
		c.root.none = true
	case map[string]any:
		c.root.base, _ = r["base"].(string)
		c.root.glob, _ = r["pattern"].(string)
	}
	list, _ := result.Get("sources").Export().([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		src := Source{}
		src.Base, _ = m["base"].(string)
		src.Pattern, _ = m["pattern"].(string)
		src.Negated, _ = m["negated"].(bool)
		c.sources = append(c.sources, src)
	}
	return c, nil
}

// buildCSS generates the stylesheet for the given candidates. Like
// Tailwind's build(), it is incremental: earlier candidates stay included.
func (c *compiled) buildCSS(candidates []string) (string, error) {
	v, err := c.build(goja.Undefined(), c.eng.vm.ToValue(candidates))
	if err != nil {
		return "", fmt.Errorf("tailwind: build: %w", err)
	}
	return v.String(), nil
}

// loadStylesheet resolves an @import. "tailwindcss" and its sub-paths, and
// the packages in packages, come from the embedded files; relative and
// absolute paths come from disk. Other packages are looked up in
// node_modules if one exists, so further CSS-only packages keep working
// when they are installed.
func loadStylesheet(id, base string) (resolvedPath, newBase, content string, err error) {
	if name, ok := embeddedName(id, base); ok {
		b, err := dist.ReadFile("dist/" + name)
		if err != nil {
			return "", "", "", fmt.Errorf("tailwindcss/%s: not part of the embedded package", name)
		}
		return embeddedBase + name, embeddedBase, string(b), nil
	}
	if name, ok := packages[id]; ok {
		b, err := dist.ReadFile("dist/" + name)
		if err != nil {
			return "", "", "", err
		}
		return embeddedBase + name, embeddedBase, string(b), nil
	}
	if base == embeddedBase {
		return "", "", "", fmt.Errorf("cannot resolve %q from an embedded package", id)
	}

	var candidates []string
	switch {
	case filepath.IsAbs(id):
		candidates = []string{id}
	case strings.HasPrefix(id, "./") || strings.HasPrefix(id, "../"):
		candidates = []string{filepath.Join(base, id)}
	default:
		// Bare package import: walk up looking for node_modules/<id>.
		for dir := base; ; dir = filepath.Dir(dir) {
			p := filepath.Join(dir, "node_modules", filepath.FromSlash(id))
			candidates = append(candidates, p, filepath.Join(p, "index.css"), p+".css")
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	for _, p := range candidates {
		if !strings.HasSuffix(p, ".css") && !fileExists(p) {
			p += ".css"
		}
		if b, err := os.ReadFile(p); err == nil {
			return p, filepath.Dir(p), string(b), nil
		}
	}
	return "", "", "", fmt.Errorf("cannot find stylesheet %q (from %s)", id, base)
}

// embeddedName maps an import of the tailwindcss package to a file in dist.
func embeddedName(id, base string) (string, bool) {
	switch id {
	case "tailwindcss", "tailwindcss/index", "tailwindcss/index.css":
		return "index.css", true
	case "tailwindcss/theme", "tailwindcss/theme.css":
		return "theme.css", true
	case "tailwindcss/preflight", "tailwindcss/preflight.css":
		return "preflight.css", true
	case "tailwindcss/utilities", "tailwindcss/utilities.css":
		return "utilities.css", true
	}
	if base == embeddedBase && strings.HasPrefix(id, "./") {
		return path.Clean(strings.TrimPrefix(id, "./")), true
	}
	return "", false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
