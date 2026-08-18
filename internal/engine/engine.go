// Package engine analyzes Go source against rust-style comment conventions
// and produces text edits that are byte-stable under gofmt.
package engine

/// <!- FILE: `engine.go`
/// <!- PURPOSE: Analyze Go source for comment-style violations; emit fix edits that stay gofmt-stable.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Rule string

const (
	RFuncDoc     Rule = "func-doc"
	RDetach      Rule = "detach"
	RTagSlash    Rule = "tag-slash"
	RBlock       Rule = "block-comment"
	RAscii       Rule = "ascii"
	RHeadingFile Rule = "heading-file"
	RHeadingPurp Rule = "heading-purpose"
	RSlop        Rule = "slop"
)

type Violation struct {
	Line    int
	EndLine int /// inclusive end line for multi-line violations; 0 means single-line
	Rule    Rule
	Message string /// imperative fix instruction, agent-actionable
	Fixable bool
}

type Edit struct {
	Start, End int
	New        string
}

type Result struct {
	Violations []Violation
	Edits      []Edit
}

var (
	genRe       = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)
	directiveRe = regexp.MustCompile(`^(go:|nolint|swagger:|easyjson:)`)
	headingRe   = regexp.MustCompile(`^\s*//+/?\s*<!-\s*(FILE|PURPOSE)\s*:`)
	slopRe      = regexp.MustCompile(`(?i)^(this function|helper (to |function )|note that|simply |in order to )| end of | is a helper`)
)

var asciiMap = map[rune]string{
	'\u2014': "-", '\u2013': "-",
	'\u2192': "->", '\u21D2': "->", '\u27F6': "->",
	'\u2190': "<-", '\u21D0': "<-",
	'\u201C': `"`, '\u201D': `"`, '\u2018': "'", '\u2019': "'",
	'\u2026': "...",
	'\u00B1': "+/-",
	'\u00D7': "*", '\u00F7': "/",
	'\u2264': "<=", '\u2265': ">=", '\u2260': "!=",
	'\u2022': "-", '\u00B7': ".",
}

func IsGenerated(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.HasPrefix(t, "package ") {
			return false
		}
		if genRe.MatchString(t) {
			return true
		}
	}
	return false
}

func asciiPairs(s string) string {
	seen := map[rune]bool{}
	var parts []string
	for _, r := range s {
		if repl, ok := asciiMap[r]; ok && !seen[r] {
			seen[r] = true
			parts = append(parts, fmt.Sprintf("%c -> %s", r, repl))
		}
	}
	return strings.Join(parts, ", ")
}

func mapASCII(s string) (string, bool) {
	var b strings.Builder
	changed := false
	for _, r := range s {
		if r < 128 {
			b.WriteRune(r)
			continue
		}
		if repl, found := asciiMap[r]; found {
			b.WriteString(repl)
			changed = true
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), changed
}

func isSwaggerOrDirective(g *ast.CommentGroup) bool {
	for _, c := range g.List {
		if !strings.HasPrefix(c.Text, "//") {
			continue
		}
		t := strings.TrimSpace(c.Text[2:])
		if strings.HasPrefix(t, "@") || directiveRe.MatchString(t) {
			return true
		}
	}
	return false
}

func Analyze(src []byte, filename string) (*Result, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	tf := fset.File(f.Package)
	off := func(p token.Pos) int { return tf.Offset(p) }
	r := &Result{}

	type span struct{ lo, hi int }
	var deleted []span
	inDeleted := func(lo int) bool {
		for _, s := range deleted {
			if lo >= s.lo && lo < s.hi {
				return true
			}
		}
		return false
	}

	// Annotation zones: struct/interface bodies and parenthesized const/var blocks.
	type zone struct{ lo, hi token.Pos }
	var zones []zone
	ast.Inspect(f, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.StructType:
			if t.Fields != nil {
				zones = append(zones, zone{t.Fields.Pos(), t.Fields.End()})
			}
		case *ast.InterfaceType:
			if t.Methods != nil {
				zones = append(zones, zone{t.Methods.Pos(), t.Methods.End()})
			}
		case *ast.GenDecl:
			if t.Tok != token.IMPORT && t.Lparen.IsValid() && t.Rparen.IsValid() {
				zones = append(zones, zone{t.Lparen, t.Rparen})
			}
		}
		return true
	})
	inZone := func(p token.Pos) bool {
		for _, z := range zones {
			if p >= z.lo && p < z.hi {
				return true
			}
		}
		return false
	}

	// Rule A: delete non-swagger comment groups attached to funcs.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Doc == nil {
			continue
		}
		if isSwaggerOrDirective(fd.Doc) {
			continue
		}
		msg := "delete this comment group above the func (rule: no comments above funcs except swagger)"
		for _, c := range fd.Doc.List {
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(c.Text, "//")), "<!- FORMULA") {
				msg = "delete this comment group above the func; first move any <!- FORMULA lines into the function body as a detached /// block"
				break
			}
		}
		lo, hi := off(fd.Doc.Pos()), off(fd.Doc.End())
		r.Violations = append(r.Violations, Violation{Line: tf.Line(fd.Doc.Pos()), EndLine: tf.Line(fd.Doc.End()), Rule: RFuncDoc, Message: msg, Fixable: true})
		r.Edits = append(r.Edits, Edit{lo, hi, ""})
		deleted = append(deleted, span{lo, hi})
	}

	// Map of comment groups that are the Doc of a top-level GenDecl (type/const/var/import).
	// Used so rule C, when it rewrites an attached // tag to ///, also detaches the decl
	// (otherwise gofmt would rewrite the /// it just created).
	genDocDecls := map[*ast.CommentGroup]*ast.GenDecl{}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Doc != nil {
			genDocDecls[g.Doc] = g
		}
	}

	// detachIfAttached inserts a blank line before the decl when the comment group
	// being rewritten to /// is the attached Doc of a GenDecl; gofmt would otherwise
	// rewrite the /// back to // / (rule 7).
	detachIfAttached := func(g *ast.CommentGroup) {
		gd, ok := genDocDecls[g]
		if !ok || tf.Line(gd.Pos())-tf.Line(gd.Doc.End()) >= 2 {
			return
		}
		p := off(gd.Pos())
		r.Violations = append(r.Violations, Violation{Line: tf.Line(gd.Doc.Pos()), Rule: RDetach, Message: fmt.Sprintf("insert one blank line before line %d - /// block must be detached from the %s decl", tf.Line(gd.Pos()), gd.Tok.String()), Fixable: true})
		r.Edits = append(r.Edits, Edit{p, p, "\n"})
	}

	// Rule B: detach /// blocks attached to top-level type/const/var decls.
	// Rule B: detach /// blocks attached to top-level type/const/var decls.
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Doc == nil {
			continue
		}
		if !strings.HasPrefix(g.Doc.List[0].Text, "///") {
			continue
		}
		if tf.Line(g.Pos())-tf.Line(g.Doc.End()) >= 2 {
			continue
		}
		p := off(g.Pos())
		r.Violations = append(r.Violations, Violation{Line: tf.Line(g.Doc.Pos()), Rule: RDetach, Message: fmt.Sprintf("insert one blank line before line %d - /// block must be detached from the %s decl", tf.Line(g.Pos()), g.Tok.String()), Fixable: true})
		r.Edits = append(r.Edits, Edit{p, p, "\n"})
	}

	// Rules C/D/E over every comment token.
	for _, g := range f.Comments {
		for _, c := range g.List {
			lo, hi := off(c.Pos()), off(c.End())
			if inDeleted(lo) {
				continue
			}

			if strings.HasPrefix(c.Text, "/*") {
				content := strings.TrimSuffix(strings.TrimPrefix(c.Text, "/*"), "*/")
				var lines []string
				for _, ln := range strings.Split(content, "\n") {
					ln = strings.TrimSpace(strings.Trim(ln, " \t*"))
					if ln != "" {
						lines = append(lines, "/// "+ln)
					}
				}
				rep := ""
				if len(lines) > 0 {
					lineStart := strings.LastIndexByte(string(src[:lo]), '\n') + 1
					indent := string(src[lineStart:lo])
					rep = strings.Join(lines, "\n"+indent)
				}
				r.Violations = append(r.Violations, Violation{Line: tf.Line(c.Pos()), EndLine: tf.Line(c.End()), Rule: RBlock, Message: "rewrite /* */ block as /// line comments", Fixable: true})
				r.Edits = append(r.Edits, Edit{lo, hi, rep})
				if rep != "" {
					detachIfAttached(g)
				}
				continue
			}

			body := c.Text[2:]
			trimmed := strings.TrimSpace(body)
			if trimmed == "" {
				continue
			}
			if directiveRe.MatchString(trimmed) || strings.HasPrefix(trimmed, "@") {
				continue
			}

			needSlash := strings.HasPrefix(trimmed, "<!-") || inZone(c.Pos())
			if needSlash && !strings.HasPrefix(c.Text, "///") {
				r.Violations = append(r.Violations, Violation{Line: tf.Line(c.Pos()), Rule: RTagSlash, Message: "rewrite comment prefix // to /// (annotation/tag)", Fixable: true})
				r.Edits = append(r.Edits, Edit{lo, lo + 2, "///"})
				detachIfAttached(g)
			}

			if nb, changed := mapASCII(body); changed {
				r.Violations = append(r.Violations, Violation{Line: tf.Line(c.Pos()), Rule: RAscii, Message: "replace non-ASCII symbols in comment: " + asciiPairs(body), Fixable: true})
				r.Edits = append(r.Edits, Edit{lo + 2, hi, nb})
			}

			if slopRe.MatchString(trimmed) {
				r.Violations = append(r.Violations, Violation{Line: tf.Line(c.Pos()), Rule: RSlop, Message: "candidate deletion: comment restates the code; delete if self-explanatory", Fixable: false})
			}
		}
	}

	// Rule F: file heading between package and import.
	pkgEnd := off(f.Name.End())
	if nl := strings.IndexByte(string(src[pkgEnd:]), '\n'); nl >= 0 {
		pkgEnd += nl + 1
	}
	zoneEnd := len(src)
	if len(f.Imports) > 0 {
		zoneEnd = off(f.Imports[0].Pos())
	} else if len(f.Decls) > 0 {
		zoneEnd = off(f.Decls[0].Pos())
	}

	hasFILE, hasPURPOSE, purposeIsTODO := false, false, false
	fileGroupEnd := -1
	for _, g := range f.Comments {
		glo, ghi := off(g.Pos()), off(g.End())
		if glo < pkgEnd || ghi > zoneEnd {
			continue
		}
		for _, c := range g.List {
			if m := headingRe.FindStringSubmatch(c.Text); m != nil {
				switch m[1] {
				case "FILE":
					hasFILE = true
					fileGroupEnd = ghi
				case "PURPOSE":
					hasPURPOSE = true
					if strings.Contains(c.Text, "TODO") {
						purposeIsTODO = true
					}
				}
			}
		}
	}

	if !hasFILE || !hasPURPOSE {
		var b strings.Builder
		if !hasFILE {
			b.WriteString("/// <!- FILE: `" + filepath.Base(filename) + "`\n")
			r.Violations = append(r.Violations, Violation{Line: tf.Line(f.Pos()), Rule: RHeadingFile, Message: "insert heading line after package line: /// <!- FILE: `" + filepath.Base(filename) + "`", Fixable: true})
		}
		if !hasPURPOSE {
			b.WriteString("/// <!- PURPOSE: TODO - fill in.\n")
			r.Violations = append(r.Violations, Violation{Line: tf.Line(f.Pos()), Rule: RHeadingPurp, Message: "insert heading line after FILE: /// <!- PURPOSE: <one-line purpose> (fails check until filled)", Fixable: true})
		}
		insAt, rep := pkgEnd, "\n"+b.String()
		if hasFILE && fileGroupEnd >= 0 {
			// FILE exists, PURPOSE missing: insert right after the FILE group's line.
			if nl := strings.IndexByte(string(src[fileGroupEnd:]), '\n'); nl >= 0 {
				insAt = fileGroupEnd + nl + 1
				rep = b.String()
			}
		}
		r.Edits = append(r.Edits, Edit{insAt, insAt, rep})
	}
	if purposeIsTODO {
		r.Violations = append(r.Violations, Violation{Line: tf.Line(f.Pos()), Rule: RHeadingPurp, Message: "replace TODO placeholder in PURPOSE with a real one-line purpose", Fixable: false})
	}

	sort.Slice(r.Edits, func(i, j int) bool {
		if r.Edits[i].Start != r.Edits[j].Start {
			return r.Edits[i].Start < r.Edits[j].Start
		}
		return r.Edits[i].End < r.Edits[j].End
	})
	return r, nil
}

func Apply(src []byte, edits []Edit) []byte {
	if len(edits) == 0 {
		return src
	}
	out := make([]byte, len(src))
	copy(out, src)
	prevEnd := -1
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		if e.Start < 0 || e.End > len(out) || e.Start > e.End {
			continue
		}
		if prevEnd >= 0 && e.End > prevEnd {
			continue // overlap guard, should not happen
		}
		prevEnd = e.Start
		out = append(out[:e.Start], append([]byte(e.New), out[e.End:]...)...)
	}
	for {
		replaced := strings.ReplaceAll(string(out), "\n\n\n", "\n\n")
		if replaced == string(out) {
			return []byte(replaced)
		}
		out = []byte(replaced)
	}
}
