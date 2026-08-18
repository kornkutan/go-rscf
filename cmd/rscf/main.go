package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kornkutan/go-rscf/internal/engine"
)

func main() {
	var fix, diff bool
	var paths []string
	for _, a := range os.Args[1:] {
		switch a {
		case "--fix":
			fix = true
		case "--diff":
			diff = true
		case "-h", "--help":
			usage()
			return
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "unknown flag %q\n", a)
				usage()
				os.Exit(2)
			}
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		usage()
		os.Exit(2)
	}
	if fix && diff {
		fmt.Fprintln(os.Stderr, "--fix and --diff are mutually exclusive")
		os.Exit(2)
	}
	mode := "dry-run"
	if fix {
		mode = "fix"
	} else if diff {
		mode = "diff"
	}

	files := collectFiles(paths)
	sort.Strings(files)

	ruleCounts := map[engine.Rule]int{}
	var reportOnly []string
	changed, skippedGen, parseErrs, unstable := 0, 0, 0, 0
	tmp, _ := os.MkdirTemp("", "rscf-")
	defer os.RemoveAll(tmp)

	printedHint := false
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", f, err)
			continue
		}
		if engine.IsGenerated(src) {
			skippedGen++
			continue
		}
		res, err := engine.Analyze(src, f)
		if err != nil {
			parseErrs++
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", f, err)
			continue
		}
		for _, v := range res.Violations {
			ruleCounts[v.Rule]++
			if !v.Fixable {
				reportOnly = append(reportOnly, fmt.Sprintf("%s:%d [%s] %s", rel(f), v.Line, v.Rule, v.Message))
			}
		}
		if len(res.Edits) == 0 {
			continue
		}
		out := engine.Apply(src, res.Edits)
		fmtOut, ferr := format.Source(out)
		stable := ferr == nil && bytes.Equal(fmtOut, out)
		if !stable {
			unstable++
		}
		changed++

		switch mode {
		case "dry-run":
			if !printedHint {
				printedHint = true
				fmt.Println("# apply fixes bottom-up within each file to keep line numbers valid")
			}
			for _, v := range res.Violations {
				if !v.Fixable {
					continue
				}
				if v.EndLine > v.Line {
					fmt.Printf("%s:%d-%d [%s] %s\n", rel(f), v.Line, v.EndLine, v.Rule, v.Message)
				} else {
					fmt.Printf("%s:%d [%s] %s\n", rel(f), v.Line, v.Rule, v.Message)
				}
			}
			if !stable {
				fmt.Printf("%s: [gofmt-unstable] rewrite is not gofmt-stable; engine bug, file an issue\n", rel(f))
			}
		case "diff":
			aPath := filepath.Join(tmp, "a")
			bPath := filepath.Join(tmp, "b")
			os.WriteFile(aPath, src, 0644)
			os.WriteFile(bPath, out, 0644)
			cmd := exec.Command("diff", "-u",
				"-L", "a/"+rel(f),
				"-L", "b/"+rel(f),
				aPath, bPath)
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Run() // exit 1 means differences exist
			os.Stdout.Write(buf.Bytes())
			if !stable {
				fmt.Printf("[gofmt-unstable] %s\n", rel(f))
			}
		case "fix":
			if !stable {
				fmt.Printf("[gofmt-unstable, NOT written] %s\n", rel(f))
				continue
			}
			if err := os.WriteFile(f, out, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "write %s: %v\n", f, err)
				continue
			}
			fmt.Printf("fixed %s\n", rel(f))
		}
	}

	fmt.Printf("\n== summary ==\n")
	fmt.Printf("scanned: %d, generated skipped: %d, parse errors: %d\n", len(files), skippedGen, parseErrs)
	fmt.Printf("files with violations: %d, gofmt-unstable: %d\n", changed, unstable)
	for _, rule := range []engine.Rule{engine.RFuncDoc, engine.RDetach, engine.RTagSlash, engine.RBlock, engine.RAscii, engine.RHeadingFile, engine.RHeadingPurp, engine.RSlop} {
		if n := ruleCounts[rule]; n > 0 {
			fmt.Printf("  %-16s %d\n", rule, n)
		}
	}
	fmt.Printf("report-only (manual review): %d\n", len(reportOnly))
	for _, l := range reportOnly {
		fmt.Printf("  %s\n", l)
	}
	if len(ruleCounts) > 0 {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: rscf [--fix|--diff] <paths>...

  rscf <paths>      dry run (default): print file:line [rule] fix instructions,
                    one per line, addressed for an AI coding agent to apply
  rscf --fix <paths> apply rewrites in place; refuses files that are not gofmt-stable
  rscf --diff <paths> unified diff of proposed rewrites (human review)`)
}

func collectFiles(roots []string) []string {
	var files []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stat %s: %v\n", root, err)
			continue
		}
		if !info.IsDir() {
			if strings.HasSuffix(root, ".go") {
				files = append(files, root)
			}
			continue
		}
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch info.Name() {
				case "vendor", ".git", "node_modules", "testdata", "bin", ".idea":
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") {
				files = append(files, p)
			}
			return nil
		})
	}
	return files
}

func rel(p string) string {
	if strings.HasPrefix(p, "./") {
		return p[2:]
	}
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}