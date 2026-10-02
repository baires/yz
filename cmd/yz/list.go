package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/baires/yz/internal/history"
	"github.com/baires/yz/internal/ui"
)

// runList prints previously recorded shares, newest first. On a terminal it
// shows an interactive table; piped output stays plain text.
func runList(env envConfig, stdout, stderr io.Writer) int {
	entries, err := history.Load(env.ConfigDir)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "no shares yet — share a file with: yz <file>")
		return 0
	}
	if ui.Interactive(os.Stdin, stdout) {
		if err := ui.RunShareList(os.Stdin, stdout, entries); err != nil {
			writeDiag(stderr, err.Error())
			return 1
		}
		return 0
	}
	now := time.Now()
	for _, e := range entries {
		status := ""
		if e.ExpiresAt != nil {
			if e.Expired(now) {
				status = " (expired)"
			} else {
				status = " (expires " + history.FormatTime(*e.ExpiresAt) + ")"
			}
		}
		_, _ = fmt.Fprintf(stdout, "%s  %8s  %s%s\n    %s\n",
			history.FormatTime(e.CreatedAt), ui.HumanSize(e.Size), ui.SafeText(e.File), status, e.URL)
	}
	return 0
}
