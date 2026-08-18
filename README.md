# rust-style-comment-formatters

`rscf` - a Go code formatter that enforces Rust-style comment conventions across Golang projects.

## Install

Requires Go 1.26+.

```
go install github.com/kornkutan/rscf/cmd/rscf@latest
```

Then from any Go repository root:

```
rscf .        # dry run (default): print fix instructions for each violation
rscf --fix .  # apply
rscf --diff . # unified diff of proposed rewrites
```

## Objective

A Go code formatter that enforces Rust-style comment conventions across Golang projects.

## Objective

Automate the following comment rules:

### Triple-Slash Doc Comments

All structured comments use `///` (triple-slash, Rust-style). No `//` for doc annotations.

### File Heading

Every `.go` file has a heading block below `package` and above `import`.

```go
package commands

/// <!- FILE: `create_order.go`
/// <!- PURPOSE: CQRS Command handler - creates new order records.

import (
```

Tags: `FILE`, `PURPOSE`. Always present. `REMARK` is optional for additional context.

```go
/// <!- FILE: `reader.go`
/// <!- PURPOSE: Repository read interface - defines query operations.
/// <!- REMARK: Database-agnostic interface; implementations provided by adapters.
```

### Section Tags in Structs and Logic

Use `/// <!- <TAG>: <BODY>` to annotate grouped fields in structs and important logic blocks in functions.

```go
type CalculateInput struct {
    /// <!- BORROWER PROFILE
    MemberID   string
    Salary     money.Money

    /// <!- LOAN ELIGIBILITY
    Principal  money.Money
    Rate       rate.Pct

    /// <!- CALCULATION MODE
    Mode       *string
    Terms      *int
}
```

```go
/// <!- CALCULATE: Insurance coverage
/// <!- FORMULA: coverage = MAX(0, principal - welfare - share_value)
insuranceCoverage := input.Principal
```

### Tag Reference

| Tag         | Used For                           | Example                                                             |
|-------------|------------------------------------|---------------------------------------------------------------------|
| `FILE`      | File heading (filename)            | `/// <!- FILE: calculate_payment.go`                                |
| `PURPOSE`   | File heading (what it does)        | `/// <!- PURPOSE: CQRS Command handler - calculates payment.`       |
| `REMARK`    | Additional context, caveats        | `/// <!- REMARK: Database-agnostic; adapters in infra/db/.`         |
| `FORMULA`   | Financial/business formula         | `/// <!- FORMULA: M = P * [r(1+r)^n] / [(1+r)^n - 1]`               |
| Section tag | Struct field grouping, logic block | `/// <!- BORROWER PROFILE`, `/// <!- CALCULATE: Term interest rate` |

### Rules

1. No self-explanatory comments. If the code says what it does, do not repeat it in a comment.
2. `FORMULA` tag is mandatory for financial/business formulas. No exceptions.
3. Section tags are for grouping. Use UPPER_SNAKE_CASE for the label.
4. Inline `///` is allowed on the same line as code for short annotations:

   ```go
   AmortizationType string /// "equal_principal" | "equal_payment" | "balloon"
   ```

5. Never use `/* */` block comments for doc annotations. Use `///`.

6. ASCII only in comments and source code. No Unicode symbols that cannot be typed on a standard keyboard. Use `->` for right arrow, `-` for em dash, and `"` for smart quotes. This applies to all doc comments, comments, swagger annotations, and inline comments.

7. `///` marker blocks must be DETACHED from top-level declarations - gofmt normalizes decl-attached `///` to `// /` (verified Go 1.26). The rewrite hits only comments Go treats as the doc of a declaration: directly above `func`, `type`, top-level `const X`/`var X`, and the `const(`/`var(`/`type` keyword. Everything else survives byte-exact with no blank line needed: `///` above a spec inside `const (...)`/`var (...)`, above struct fields, inside function bodies, `import` headers, standalone dividers, inline trailing `///`.

8. Do not write comments that can trace information straight back to spec documentation or reference in technical terms such as `Section A`, `Section 1.2`, etc. Just explain it directly if needed.

## The Function Rule

No `//` doc comments directly above public or private functions. If a function needs a note, put it inside the body as a detached `///` block or a section tag.

Rationale: gofmt normalizes decl-attached `///` into `// /`, so triple-slash cannot survive on declarations anyway. Dropping the doc comment entirely keeps the tree clean and forces the comment to live next to the logic it explains.

**Exception:** Swagger annotations in API handlers (`// @Summary`, `// @Router`, etc.) stay as `//` above the handler func. Swaggo parses them from decl-attached `//` comments; there is no alternative syntax.

## Enforcement Policy

The formatter is a gatekeeper against AI slop comments. Strict by default: `check` fails on any comment not in a sanctioned shape; `fix` rewrites or deletes it.

| Violation | `check` | `fix` |
|---|---|---|
| `//` or `///` above a func, non-swagger | fail | delete |
| `// @...` swagger group above handler | pass | keep |
| `///` attached to `type`/`const`/`var` decl | fail | insert blank line (detach) |
| plain `//` godoc above `type`/`const`/`var` | pass | keep |
| `//` used as annotation/section tag in structs, const/var blocks, bodies | fail | rewrite to `///` |
| `/* */` doc comment | fail | rewrite to `///` lines |
| Unicode symbols in comments | fail | ASCII normalize |
| Missing `FILE` heading | fail | inject (derived from filename) |
| Missing `PURPOSE` | fail | inject empty template, keep failing until filled |
| Restating/self-explanatory comment in a body | fail | report only, human decides |

The Function Rule is what makes de-slopping deterministic: since no comment is allowed above a func except swagger, the tool never judges whether a func doc comment is valuable. It is non-compliant by definition. Delete.

Two things the formatter never does:

1. Never auto-write `PURPOSE` prose. A formatter that invents descriptions is the slop machine it fights. Inject the tag skeleton, fail check until a human fills it.
2. Never auto-delete comments inside function bodies. Redundancy is a judgment call; report candidates, let the human pull the trigger.

Slop signatures reported in the report-only lane: comment begins with the next declaration's identifier, "This function", "Helper to", "Note that", "Simply", trailing "// end of" markers, emoji.

## Commands

Dry run is the default. Two options:

- `rscf <paths>` - dry run (default). Prints each violation as `file:start[-end] [rule] <imperative fix instruction>`, one per line, addressed so an AI coding agent can apply them directly. Read-only, exit 1 on any violation. CI gate.
- `rscf --fix <paths>` - apply rewrites in place. Refuses to write any file whose output is not gofmt-stable.
- `rscf --diff <paths>` - unified diff of the proposed rewrites. Human review; the agent instructions are the machine-facing format, this is the eye-facing one.

Instruction format:

```
path/to/file.go:234-239 [func-doc] delete this comment group above the func; first move any <!- FORMULA lines into the function body as a detached /// block
path/to/file.go:26 [tag-slash] rewrite comment prefix // to /// (annotation/tag)
path/to/file.go:1 [heading-file] insert heading line after package line: /// <!- FILE: `file.go`
```

The first output line carries a standing hint: apply fixes bottom-up within each file so earlier edits do not shift the line numbers of later ones.

Implementation guarantee: dry-run, `--fix`, and `--diff` share the exact same rewrite pipeline - only the output step differs. Never two separate paths, or dry-run shows one thing and fix writes another.

## Skipped Files

Files matching the Go generated-file marker are skipped entirely - no rules run, no edits, no diff. A generated file is overwritten on the next generation run, so editing it is wasted work and the diff is pure churn.

The marker, per the `go generate` spec: a line matching `^// Code generated .* DO NOT EDIT\.$` appearing before the `package` clause (after optional leading blank lines and copyright comments). Any file containing it is skipped, listed once in the summary as `generated/skipped`, and never reprinted.

This is a whole-file skip, not a line skip. There is no partial handling.

## Why This Style
Personal preference. I come from a Rust and Java background:

- Rust's `///` doc comments cleanly separate documentation from casual `//` remarks. The slash count carries meaning.
- Java's Javadoc (`/** */`) taught me that doc comments are an API contract, not noise. Most Go functions do not need one.
- Self-explanatory code should not carry a comment restating it. Comments earn their place by explaining intent, formulas, or grouping - which is exactly what the tags above encode.
