# rust-style-comment-formatters

`rscf` - a Go formatter enforcing Rust-style `///` comment conventions. Fights AI slop comments.

## Install

Requires Go 1.26+.

```
go install github.com/kornkutan/go-rscf/cmd/rscf@latest
```

```
rscf .        # dry run (default): print file:line [rule] fix instructions, exit 1 on violations
rscf --fix .  # apply rewrites; refuses files that are not gofmt-stable
rscf --diff . # unified diff of proposed rewrites
```

Dry-run instructions are addressed (`path:line[-end] [rule] <imperative instruction>`) so an AI coding agent can apply them directly. All three modes share one rewrite pipeline - only the output step differs.

## Rules

All structured comments use `///`. No `//` for doc annotations.

Every `.go` file has a heading between `package` and `import`:

```go
package commands

/// <!- FILE: `create_order.go`
/// <!- PURPOSE: CQRS Command handler - creates new order records.

import (
```

`/// <!- <TAG>: <BODY>` annotates grouped struct fields and logic blocks:

```go
type CalculateInput struct {
    /// <!- BORROWER PROFILE
    MemberID string

    /// <!- LOAN ELIGIBILITY
    Principal money.Money
    Rate      rate.Pct
}
```

| Tag         | Used for                              | Example                                    |
|-------------|---------------------------------------|--------------------------------------------|
| `FILE`      | File heading (filename)               | `/// <!- FILE: calculate_payment.go`       |
| `PURPOSE`   | File heading (what it does)           | `/// <!- PURPOSE: CQRS Command handler.`   |
| `REMARK`    | Optional extra context                | `/// <!- REMARK: Database-agnostic.`       |
| `FORMULA`   | Financial/business formula            | `/// <!- FORMULA: M = P * [r(1+r)^n]`      |
| Section tag | Field grouping, logic block           | `/// <!- BORROWER PROFILE`                 |

1. No self-explanatory comments. If the code says it, do not repeat it.
2. `FORMULA` is mandatory for financial/business formulas.
3. Section tag labels use UPPER_SNAKE_CASE.
4. Inline `///` allowed: `AmortizationType string /// "balloon"`. Every comment is `///` - annotations, tags, remarks, parked code. `//` survives only where tooling forces it: the package doc clause (gofmt rewrites `///` to `// /` there), swagger `@` groups, and compiler directives (`//go:`, `//nolint`).
5. Never `/* */` for doc annotations.
6. ASCII symbols. `->` for arrows, `-` for em dash, `"` for smart quotes. Human-language scripts (Thai, etc.) pass through untouched.
7. `///` must be DETACHED from top-level decls - gofmt rewrites decl-attached `///` to `// /` (verified Go 1.26). Applies to comments directly above `func`, `type`, top-level `const X`/`var X`, and the `const(`/`var(`/`type` keyword. `///` inside `const (...)`/`var (...)` blocks, above struct fields, and in function bodies survives untouched.
8. No comments tracing back to spec docs in technical terms (`Section A`, `Section 1.2`). Explain directly.

## The Function Rule

No `//` or `///` comments above public or private functions. Put notes inside the body as a detached `///` block. Exception: swagger annotations (`// @Summary`, `// @Router`, ...) stay as `//` above the handler func - swaggo has no alternative syntax.

## Enforcement

| Violation                                              | check | fix                                 |
|--------------------------------------------------------|-------|-------------------------------------|
| `//` or `///` above a func, non-swagger                | fail  | delete                              |
| `// @...` swagger group                                | pass  | keep                                |
| `///` attached to `type`/`const`/`var` decl            | fail  | insert blank line                   |
| plain `//` godoc above `type`/`const`/`var`            | fail  | rewrite to `///` + detach           |
| any other `//` comment (body, trailing, floating)      | fail  | rewrite to `///`                    |
| `///` above the `package` clause                       | fail  | rewrite to `//` (gofmt constraint)  |
| `/* */` doc comment                                    | fail  | rewrite to `///` lines              |
| Unicode symbols in comments                            | fail  | ASCII normalize                     |
| Missing `FILE` heading                                 | fail  | inject (from filename)              |
| Missing `PURPOSE`                                      | fail  | inject template, fails until filled |
| Restating comment in a body                            | fail  | report only                         |

The formatter never auto-writes `PURPOSE` prose (a formatter inventing descriptions is the slop machine it fights) and never auto-deletes body comments (report candidates, human decides).

Files with `^// Code generated .* DO NOT EDIT\.$` before the `package` clause are skipped entirely.

## Why This Style

Personal preference. I come from a Rust and Java background:

- Rust's `///` cleanly separates docs from casual `//` remarks. The slash count carries meaning.
- Java's Javadoc taught me doc comments are an API contract, not noise. Most Go functions do not need one.
- Comments earn their place explaining intent, formulas, or grouping - which is what the tags encode.

## License

MIT
