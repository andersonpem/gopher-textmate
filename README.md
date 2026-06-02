# gopher-textmate

A pure-Go (no cgo) [TextMate grammar](https://macromates.com/manual/en/language_grammars) interpreter for syntax highlighting, designed to be embedded in terminal applications.

It tokenizes source text into scoped tokens using TextMate grammars, resolves a VSCode-style JSON theme, and renders ANSI (truecolor / 256-color) output. The tokenizer follows the algorithm used by [`vscode-textmate`](https://github.com/microsoft/vscode-textmate), the de-facto reference implementation.

<img width="1132" height="431" alt="php" src="https://github.com/user-attachments/assets/4b67b298-f9d1-453c-a139-6e9dca97730b" />


## Why pure Go?

TextMate grammars rely on Oniguruma regular expressions (lookbehind, lookahead, `\G`, back-references, `\x{...}` codepoints) that Go's standard `regexp` (RE2) cannot handle. Instead of binding to Oniguruma via cgo, this library uses the pure-Go [`github.com/dlclark/regexp2/v2`](https://github.com/dlclark/regexp2) engine, so builds stay static and cross-compile cleanly. Oniguruma possessive quantifiers (`a++`) are rewritten as atomic groups (`(?>a+)`) to preserve their no-backtracking semantics.

## Install

```bash
go get github.com/andersonpem/gopher-textmate
```

## Library usage

The root package `textmate` is the high-level facade. Other applications import it directly:

```go
package main

import (
    "fmt"

    textmate "github.com/andersonpem/gopher-textmate"
    "github.com/andersonpem/gopher-textmate/render"
)

func main() {
    h, _ := textmate.New(textmate.WithColorMode(render.TrueColor))
    _, _ = h.LoadGrammarFile("grammars/php.tmLanguage.json")
    _ = h.SetThemeBytes(textmate.DefaultThemeBytes())

    out, _ := h.Highlight("source.php", `<?php echo "hi";`)
    fmt.Print(out)
}
```

### Two layers

- **Tokenize only** — for apps (e.g. a TUI) that do their own coloring:

```go
lines, _ := h.Tokenize("source.php", src) // []textmate.Line, Line = []grammar.Token
for _, line := range lines {
    for _, tok := range line {
        // tok.Start, tok.End are rune offsets; tok.Scopes is outer..inner
    }
}
```

  For incremental / streaming tokenization, carry state across lines yourself:

```go
var state *grammar.StateStack
for _, line := range strings.Split(src, "\n") {
    var toks textmate.Line
    toks, state, _ = h.TokenizeLine("source.php", line, state)
    _ = toks
}
```

- **Full pipeline** — `Highlight` / `HighlightTo(w, …)` produce ready-made ANSI.

### Incremental highlighting (REPLs / editors)

For type-as-you-go repainting, use a `Document`. It caches each line's tokens and tokenizer state and, on edit, only re-tokenizes the changed line(s) until the state reconverges — so a keystroke costs about one line of work, not the whole buffer.

```go
h, _ := textmate.New(textmate.WithColorMode(render.TrueColor))
_, _ = h.LoadGrammarFile("grammars/php.tmLanguage.json")
_ = h.SetThemeBytes(textmate.DefaultThemeBytes())

// Move the one-time pattern compilation off the keystroke path.
go h.Warmup("source.php")

doc, _ := h.NewDocument("source.php")
doc.SetText(buffer)            // initial tokenize

// On each keystroke:
changed := doc.SetLine(cursorLine, newLineText) // returns only the lines that changed
for _, i := range changed {
    repaint(i, doc.RenderLine(i))               // or use doc.Render() for the whole buffer
}
```

### Performance

Measured on an Macbook tokenizing a ~500-line PHP file (`go test -bench .`):

| Operation                                  | Cost          |
| ------------------------------------------ | ------------- |
| Warm-up (compile all patterns, parallel)   | ~350 ms, once |
| Re-tokenize whole 500-line buffer          | ~44 ms        |
| Incremental keystroke (`Document.SetLine`) | **~20 µs**    |

Notes:

- Run `Warmup` once at startup (ideally in a goroutine). Pattern compilation is the main one-time cost; matching is fast.
- Tokens share immutable scope slices and begin/end scanners are cached, keeping per-line allocations low.

### Concurrency

A `Highlighter` is safe for concurrent tokenization once all `LoadGrammar*` / `SetTheme*` calls are done (setup-then-use). Call `Warmup` (or an initial `Tokenize`) before sharing across goroutines. A `Document` is single-goroutine (confine it to your UI loop).

### Lower-level packages

The facade is built on exported packages you can use directly:

| Package   | Responsibility                                      |
| --------- | --------------------------------------------------- |
| `grammar` | Grammar registry, rule compilation, `TokenizeLine`  |
| `theme`   | VSCode JSON theme parsing + scope-selector matching |
| `render`  | Tokens + theme → ANSI (truecolor / 256 / none)      |
| `oniglib` | regexp2-backed scanner, anchor handling, back-refs  |

## CLI

```bash
go run ./cmd/gtm -grammar grammars/php.tmLanguage.json -scope source.php examples/sample.php
```
<img width="1132" height="431" alt="php" src="https://github.com/user-attachments/assets/4b67b298-f9d1-453c-a139-6e9dca97730b" />

Flags:

- `-grammar PATH` — grammar file (repeatable, for cross-grammar embedding)
- `-scope NAME` — scope to tokenize with (default: first grammar loaded)
- `-theme PATH` — VSCode JSON theme (default: bundled dark theme)
- `-color auto|truecolor|256|none` — color mode (default `auto`, via `COLORTERM`/`TERM`/`NO_COLOR`)
- `-bg` — also emit token background colors

## Supported grammar features

`match`, `begin`/`end`, `begin`/`while`, `include` (`#repo`, `$self`, `$base`, cross-grammar `scope.name#sub`), `repository`, `captures` / `beginCaptures` / `endCaptures` with nested `patterns`, `contentName`, `applyEndPatternLast`, dynamic end patterns via back-references (multi-digit and zero-padded, e.g. `\1`, `\12`, `\001`), `injections` (basic), and scope-name templates (`$1`, `${1}`) with the full set of TextMate transforms — `upcase`, `downcase`, `capitalize`/`titlecase`, `asciify`, `urlencode`, `shellescape`, `relative`, `number`, `duration`, `dirname`, `basename` — which may be chained, e.g. `${1:/downcase/capitalize}`.

## Known limitations

- Themes: VSCode JSON format only (`.tmTheme` plist is not yet supported).
- `$base` is treated as `$self` (identical for single-grammar tokenization).
- Cross-grammar includes only resolve grammars that have been loaded; unresolved references are skipped, so mixed-language files highlight the languages whose grammars are present.
- The rare `\g<name>` subroutine call is unsupported and such a pattern simply never matches (graceful degradation).
- `asciify` and `urlencode` transforms approximate macOS/ICU behavior (NFD + combining-mark stripping; RFC 3986 unreserved set), and `(?x)` extended-mode `#` comments containing unbalanced parentheses are not parsed.
- Injection selector matching is basic; advanced exclusion selectors degrade gracefully.

## Development

It is advisable to use the [drun](https://github.com/phillarmonic/drun) task runner for development. It makes it easy to run routine tasks in a semantic way. Check the .drun/spec.drun to understand how the file works.

```bash
# For running only the tests:
xdrun test
# For running only the linter:
xdrun lint
# For running the full test suite including the time consuming tests:
xdrun test-full
# For running the whole CI lifecycle (test, lint)
xdrun ci
```

## License

The bundled grammars under `grammars/` are derived from the
[language-php](https://github.com/KapitanOczywisty/language-php) project; see the headers in those files.
