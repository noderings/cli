package verify

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// FormatText writes a human-readable report to w.
func FormatText(w io.Writer, report *Report) error {
	if report == nil {
		return nil
	}
	if report.AgentID != "" {
		if _, err := fmt.Fprintf(w, "Provider verify (agent %s)\n", report.AgentID); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, "Provider verify"); err != nil {
			return err
		}
	}

	unicode := unicodeGlyphs(w)

	for _, sec := range report.Sections {
		if _, err := fmt.Fprintf(w, "\n[%s]\n", sec.Name); err != nil {
			return err
		}
		for _, c := range sec.Checks {
			if _, err := fmt.Fprintf(w, "  %s %s: %s\n", statusMark(c.Status, unicode), c.Name, c.Message); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if report.Passed() {
		_, err := fmt.Fprintf(w, "%s All required provider checks passed\n", statusMark(StatusPass, unicode))
		return err
	}
	_, err := fmt.Fprintf(w, "%s Provider verification failed (%d check(s))\n", statusMark(StatusFail, unicode), report.FailedCount())
	return err
}

// unicodeGlyphs reports whether the destination is a terminal that can render glyphs.
// Buffers, pipes and files get ASCII markers.
func unicodeGlyphs(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func statusMark(s Status, unicode bool) string {
	switch s {
	case StatusPass:
		if unicode {
			return "✓"
		}
		return "[OK]"
	case StatusFail:
		if unicode {
			return "✗"
		}
		return "[FAIL]"
	case StatusWarn:
		if unicode {
			return "⚠"
		}
		return "[WARN]"
	case StatusSkip:
		if unicode {
			return "–"
		}
		return "[SKIP]"
	default:
		return "?"
	}
}

// SummaryLine returns a one-line summary suitable for logs.
func SummaryLine(report *Report) string {
	if report == nil {
		return "verify: no report"
	}
	var pass, fail, warn, skip int
	for _, c := range report.FlatChecks() {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusWarn:
			warn++
		case StatusSkip:
			skip++
		}
	}
	parts := []string{
		fmt.Sprintf("pass=%d", pass),
		fmt.Sprintf("fail=%d", fail),
		fmt.Sprintf("warn=%d", warn),
		fmt.Sprintf("skip=%d", skip),
	}
	if report.Passed() {
		return "verify OK (" + strings.Join(parts, ", ") + ")"
	}
	return "verify FAILED (" + strings.Join(parts, ", ") + ")"
}
