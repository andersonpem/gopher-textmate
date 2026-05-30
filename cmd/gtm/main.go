// Command gtm highlights a source file using a TextMate grammar and a color
// theme, writing ANSI output to stdout. It is a thin wrapper over the
// github.com/andersonpem/gopher-textmate library.
//
// Usage:
//
//	gtm -grammar php.tmLanguage.json -scope source.php [-theme dark.json] [-color auto|truecolor|256|none] file.php
//
// If no file is given, input is read from stdin. If no theme is given, the
// bundled dark theme is used.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	textmate "github.com/andersonpem/gopher-textmate"
	"github.com/andersonpem/gopher-textmate/render"
)

type stringList []string

func (s *stringList) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gtm:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gtm", flag.ContinueOnError)
	var grammars stringList
	fs.Var(&grammars, "grammar", "path to a tmLanguage.json grammar (repeatable)")
	scope := fs.String("scope", "", "grammar scope to tokenize with (default: first grammar loaded)")
	themePath := fs.String("theme", "", "path to a VSCode-style JSON theme (default: bundled dark theme)")
	color := fs.String("color", "auto", "color mode: auto|truecolor|256|none")
	bg := fs.Bool("bg", false, "emit token background colors")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(grammars) == 0 {
		return fmt.Errorf("at least one -grammar is required")
	}

	mode, err := parseColorMode(*color)
	if err != nil {
		return err
	}

	h, err := textmate.New(
		textmate.WithColorMode(mode),
		textmate.WithBackground(*bg),
		textmate.WithDefaultScope(*scope),
	)
	if err != nil {
		return err
	}
	for _, gpath := range grammars {
		if _, err := h.LoadGrammarFile(gpath); err != nil {
			return err
		}
	}

	if *themePath != "" {
		if err := h.SetThemeFile(*themePath); err != nil {
			return err
		}
	} else if err := h.SetThemeBytes(textmate.DefaultThemeBytes()); err != nil {
		return err
	}

	source, err := readInput(fs.Arg(0))
	if err != nil {
		return err
	}

	out, err := h.Highlight(*scope, source)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func readInput(path string) (string, error) {
	if path == "" || path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseColorMode(s string) (render.ColorMode, error) {
	switch s {
	case "", "auto":
		return render.DetectColorMode(), nil
	case "truecolor", "24bit":
		return render.TrueColor, nil
	case "256":
		return render.Color256, nil
	case "none", "off":
		return render.None, nil
	default:
		return render.None, fmt.Errorf("unknown color mode %q (use auto|truecolor|256|none)", s)
	}
}
