package tailwind

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestGoldenProject builds testdata/project and compares the result with
// project.expected.css, which the official CLI (@tailwindcss/cli 4.1.14)
// produced from the same files:
//
//	tailwindcss --cwd testdata/project -i testdata/project/input.css -o testdata/project.expected.css
//
// The fixture exercises automatic detection, @source and @source not,
// .gitignore, node_modules, binary files, CSS-variable tracking in .css
// sources, @theme, @utility, @apply and the candidate boundary rules.
func TestGoldenProject(t *testing.T) {
	want, err := os.ReadFile("testdata/project.expected.css")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Build(Options{Input: "testdata/project/input.css", Dir: "testdata/project"})
	if err != nil {
		t.Fatal(err)
	}
	if res.CSS != string(want) {
		t.Fatalf("output differs from the official CLI\n%s", firstDiff(string(want), res.CSS))
	}
	for _, f := range res.Files {
		for _, bad := range []string{"/ignored/", "/node_modules/", "skip.txt"} {
			if strings.Contains(f, bad) {
				t.Errorf("scanned %s", f)
			}
		}
	}
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < min(len(al), len(bl)); i++ {
		if al[i] != bl[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  want " + al[i] + "\n  got  " + bl[i]
		}
	}
	return "lengths differ"
}

// TestExtractBoundaries encodes the official scanner's behavior, established
// by probing the CLI with each input: want lists the candidates it yields.
func TestExtractBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want []string // must be extracted
		not  []string // must not be extracted
	}{
		{`class="p-4"`, []string{"p-4"}, nil},
		{`class='p-4'`, []string{"p-4"}, nil},
		{"`p-4`", []string{"p-4"}, nil},
		{`<div class=p-4>`, nil, []string{"p-4"}},
		{`>p-4<`, []string{"p-4"}, nil},
		{`(p-4)`, nil, []string{"p-4"}},
		{`fn(flex, block)`, nil, []string{"flex", "block"}},
		{`flex.`, nil, []string{"flex"}},
		{`use flex, grid.`, nil, []string{"flex", "grid"}},
		{`{p-4}`, nil, []string{"p-4"}},
		{`a=p-4`, nil, []string{"p-4"}},
		{`p-4;`, nil, []string{"p-4"}},
		{`$p-4`, nil, []string{"p-4"}},
		{`a|p-4`, nil, []string{"p-4"}},
		{`p-4?`, nil, []string{"p-4"}},
		{`p-4]`, []string{"p-4"}, nil},
		{`w-[10px]]`, []string{"w-[10px]"}, nil},
		{`p-4:`, []string{"p-4"}, nil},
		{`{ 'p-4': x }`, []string{"p-4"}, nil},
		{`p-4\n`, []string{"p-4"}, nil},
		{`p-4!`, []string{"p-4!"}, []string{"p-4"}},
		{`x !p-4 y`, []string{"!p-4"}, nil},
		{`if (!container) {`, nil, []string{"!container", "container"}},
		{`x !container) y`, nil, []string{"!container"}},
		{`['p-4', 'm-2']`, []string{"p-4", "m-2"}, nil},
		{`cn("p-4",x&&"m-2")`, []string{"p-4", "m-2"}, nil},
		{"{`p-4 ${x}`}", []string{"p-4"}, nil},
		{`(bg-(--x))`, nil, []string{"bg-(--x)"}},
		{`bg-(--x)`, []string{"bg-(--x)"}, nil},
		{`"[&_svg:not([class*='size-'])]:size-4 has-[>svg]:px-3"`, []string{"[&_svg:not([class*='size-'])]:size-4", "has-[>svg]:px-3"}, nil},
		{`data-[a=b]:p-4`, []string{"data-[a=b]:p-4"}, nil},
	}
	for _, c := range cases {
		got := Extract([]byte(c.in))
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("Extract(%q) = %q, missing %q", c.in, got, w)
			}
		}
		for _, n := range c.not {
			if slices.Contains(got, n) {
				t.Errorf("Extract(%q) = %q, must not contain %q", c.in, got, n)
			}
		}
	}
}

func TestVariables(t *testing.T) {
	got := ExtractVariables([]byte(`x var(--color-a) (--color-b) "--color-c" =--color-d a--not`))
	if !slices.Equal(got, []string{"--color-a", "--color-b", "--color-c", "--color-d"}) {
		t.Fatal(got)
	}
	got = extractVarUsages([]byte(`.x { --decl: 1; color: var(--used); a: var( --spaced ); b: theme(--fn) }`))
	if !slices.Equal(got, []string{"--used"}) {
		t.Fatal(got)
	}
}

// TestEngineFixes checks the prelude's workarounds for goja bugs against
// what V8 returns.
func TestEngineFixes(t *testing.T) {
	e, err := newEngine()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`String(Number(1n << 70n))`:                       "1.1805916207174113e+21",
		`String(Number(-(1n << 64n)))`:                    "-18446744073709552000",
		`String(Number((1n<<120n)+1n - (1n<<121n)) < 0)`:  "true",
		`String(Number("12") + Number() + new Number(3))`: "15",
		`String(Number.isInteger(4) && (5 instanceof Number) === false && new Number(1) instanceof Number)`: "true",
		`[{k:1,i:0},{k:1,i:1},{k:0,i:2}].sort((a,b) => (a.k-b.k) || -0).map(x=>x.i).join()`:                 "2,0,1",
		`[3,undefined,1,,2].sort().join()`:                                     "1,2,3,,",
		`[10,9,1].sort().join()`:                                               "1,10,9",
		`String(structuredClone({a:[1,{b:new Map([[1,2]])}]}).a[1].b.get(1))`:  "2",
		`new Intl.ListFormat("en",{type:"conjunction"}).format(["a","b","c"])`: "a, b, and c",
	}
	for src, want := range cases {
		v, err := e.vm.RunString(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if v.String() != want {
			t.Errorf("%s = %s, want %s", src, v, want)
		}
	}
}

func TestGenerateAndErrors(t *testing.T) {
	css, err := Generate(`@import "tailwindcss/utilities" source(none); @theme { --spacing: 0.25rem; }`, "/", []string{"p-4", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(css, ".p-4 {") || strings.Contains(css, "nope") {
		t.Fatal(css)
	}
	if _, err := Generate(`@import "tailwindcss"; @plugin "x";`, "/", nil); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("plugin accepted: %v", err)
	}
	if _, err := Generate(`@import "./missing.css";`, t.TempDir(), nil); err == nil {
		t.Fatal("missing import accepted")
	}
}

func TestGlobAndGitignore(t *testing.T) {
	re := globRegexp("/a/**/*.{go,html}")
	for p, want := range map[string]bool{"/a/x.go": true, "/a/b/c/x.html": true, "/a/x.css": false, "/b/x.go": false} {
		if re.MatchString(p) != want {
			t.Errorf("%s: %v", p, !want)
		}
	}
	dir := t.TempDir()
	os.WriteFile(dir+"/.gitignore", []byte("dist/\n*.log\n!keep.log\n/root.txt\n"), 0o644)
	g := &gitignores{}
	g.load(dir)
	for p, want := range map[string]bool{
		dir + "/dist": true, dir + "/x/dist": true, dir + "/a.log": true, dir + "/keep.log": false,
		dir + "/root.txt": true, dir + "/sub/root.txt": false, dir + "/dist.txt": false,
	} {
		if got := g.ignored(p, !strings.Contains(p[len(dir):], ".")); got != want {
			t.Errorf("%s: ignored=%v, want %v", p, got, want)
		}
	}
}

func TestMinify(t *testing.T) {
	in := "/*! keep */\n/* drop */\n.a  ,  .b > .c {\n  color: red;\n  width: calc(1px + 2px);\n  content: \"a  b\";\n}\n.d :hover { x: y }\n"
	want := "/*! keep */\n.a,.b > .c{color:red;width:calc(1px + 2px);content:\"a  b\"}.d :hover{x:y}"
	if got := Minify(in); got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if got := Minify("a{} /* unterminated"); got != "a{}" {
		t.Fatalf("%q", got)
	}
}

func BenchmarkBuildProject(b *testing.B) {
	for b.Loop() {
		if _, err := Build(Options{Input: "testdata/project/input.css", Dir: "testdata/project"}); err != nil {
			b.Fatal(err)
		}
	}
}

// Rendered HTML escapes & < > in attributes: classes like [&_svg]:size-4
// and has-[>svg]:px-3 must still be generated.
func TestEscapedMarkup(t *testing.T) {
	dir := t.TempDir()
	html := `<button class="[&amp;_svg]:size-4 has-[&gt;svg]:px-3 [&amp;_svg:not([class*=&#39;size-&#39;])]:size-5">x</button>`
	if err := os.WriteFile(dir+"/page.html", []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Build(Options{CSS: `@import "tailwindcss" source(none); @source "./page.html";`, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`.\[\&_svg\]\:size-4`, `.has-\[\>svg\]\:px-3`, `size-\'\]\)\]\:size-5`} {
		if !strings.Contains(res.CSS, want) {
			t.Errorf("missing %s in:\n%s", want, res.CSS)
		}
	}
}
