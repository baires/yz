package ui

import (
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/term"
)

func Interactive(input io.Reader, output io.Writer) bool {
	return isTerminal(input) && isTerminal(output) && !dumb()
}

func Animated(output io.Writer) bool {
	return isTerminal(output) && !dumb()
}

func SafeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func dumb() bool {
	return os.Getenv("TERM") == "dumb"
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}
