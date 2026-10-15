// Command tailwind builds a Tailwind CSS v4 stylesheet using only Go. It is a
// drop-in for the official CLI's common flags:
//
//	go run github.com/alexisbcz/tailwind/cmd/tailwind -i styles/app.css -o static/app.css
//	go run github.com/alexisbcz/tailwind/cmd/tailwind -i styles/app.css -o static/app.css --watch
//	go run github.com/alexisbcz/tailwind/cmd/tailwind -i styles/app.css -o static/app.css --minify
//
// Typically invoked from a go:generate directive next to the go:embed that
// serves the result.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alexisbcz/tailwind"
)

func main() {
	log.SetFlags(0)
	var (
		input  = flag.String("i", "", "input stylesheet (default: @import \"tailwindcss\")")
		output = flag.String("o", "-", "output file, or - for stdout")
		dir    = flag.String("cwd", ".", "directory scanned by automatic source detection")
		minify = flag.Bool("minify", false, "minify the output")
		watch  = flag.Bool("watch", false, "rebuild when sources change")
	)
	flag.StringVar(input, "input", "", "same as -i")
	flag.StringVar(output, "output", "-", "same as -o")
	flag.BoolVar(minify, "m", false, "same as -minify")
	flag.BoolVar(watch, "w", false, "same as -watch")
	flag.Parse()

	opts := tailwind.Options{Input: *input, Dir: *dir, Minify: *minify}
	if *output != "-" {
		opts.Output = *output
	}
	if *input == "" {
		opts.CSS = `@import "tailwindcss";`
	}

	res, err := build(opts, *output)
	if err != nil {
		log.Fatal(err)
	}
	if !*watch {
		return
	}
	log.Printf("watching %d files", len(res.Files))
	watchLoop(opts, *output, res)
}

func build(opts tailwind.Options, output string) (*tailwind.Result, error) {
	start := time.Now()
	res, err := tailwind.Build(opts)
	if err != nil {
		return nil, err
	}
	if output == "-" {
		_, err = os.Stdout.WriteString(res.CSS)
		return res, err
	}
	changed, err := res.WriteFile(output)
	if err != nil {
		return nil, err
	}
	status := "unchanged"
	if changed {
		status = "written"
	}
	log.Printf("tailwind v%s: %s %s (%d files, %s)", tailwind.Version, output, status, len(res.Files), time.Since(start).Round(time.Millisecond))
	return res, nil
}

// watchLoop polls modification times: simple, portable and fast enough for
// the few hundred files of a typical project.
func watchLoop(opts tailwind.Options, output string, res *tailwind.Result) {
	outAbs, _ := filepath.Abs(output)
	snapshot := func(files []string) map[string]time.Time {
		m := make(map[string]time.Time, len(files)+1)
		for _, f := range append(files, opts.Input) {
			if f == "" {
				continue
			}
			if st, err := os.Stat(f); err == nil {
				m[f] = st.ModTime()
			}
		}
		return m
	}
	last := snapshot(res.Files)
	for range time.Tick(300 * time.Millisecond) {
		changed := false
		for f, t := range last {
			st, err := os.Stat(f)
			if err != nil || !st.ModTime().Equal(t) {
				changed = true
				break
			}
		}
		// New files only appear after a rebuild rescans the sources, so
		// also look for files newer than the last build in the watched dirs.
		if !changed {
			changed = newerFileExists(res.Files, last, outAbs)
		}
		if !changed {
			continue
		}
		next, err := build(opts, output)
		if err != nil {
			log.Print(err)
			last = snapshot(res.Files) // retry on the next change
			continue
		}
		res, last = next, snapshot(next.Files)
	}
}

// newerFileExists reports a file that is not yet watched in a directory that
// holds watched files, which is how added files are noticed.
func newerFileExists(files []string, watched map[string]time.Time, output string) bool {
	dirs := make(map[string]bool)
	for _, f := range files {
		dirs[filepath.Dir(f)] = true
	}
	for d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.ToSlash(filepath.Join(d, e.Name()))
			if _, ok := watched[p]; ok || e.IsDir() || p == filepath.ToSlash(output) {
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode().Type()&fs.ModeType == 0 && time.Since(info.ModTime()) < time.Second {
				return true
			}
		}
	}
	return false
}

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: tailwind [-i input.css] [-o output.css] [--minify] [--watch]\n\nBuilds Tailwind CSS v%s with Go only.\n\n", tailwind.Version)
		flag.PrintDefaults()
	}
}
