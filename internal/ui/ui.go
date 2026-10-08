// Package ui contains small helpers for coloured terminal output and human
// readable formatting.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fatih/color"
)

var (
	// Dir is used for directories / common prefixes and alias namespaces.
	Dir = color.New(color.FgHiBlue, color.Bold)
	// Bucket is used for bucket names.
	Bucket = color.New(color.FgHiCyan, color.Bold)
	// Object is used for regular objects.
	Object = color.New()
	// Error is used for error messages.
	Error = color.New(color.FgHiRed, color.Bold)
	// Warn is used for warnings.
	Warn = color.New(color.FgHiYellow)
	// OK is used for success messages.
	OK = color.New(color.FgHiGreen)
	// Info is used for secondary information.
	Info = color.New(color.FgHiBlack)
	// Prompt is used for the shell prompt.
	Prompt = color.New(color.FgHiGreen, color.Bold)
	// Bold is used for headings.
	Bold = color.New(color.Bold)
	// Alias is used for alias names.
	Alias = color.New(color.FgHiMagenta, color.Bold)
)

// Disable turns off all colour output.
func Disable() { color.NoColor = true }

// Force turns colour output on even when stdout is not a terminal.
func Force() { color.NoColor = false }

// HumanSize renders a byte count in a compact human readable form.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanTime renders a timestamp the way ls does.
func HumanTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// ShortTime renders a relative-ish timestamp useful in listings.
func ShortTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Local().Format("2006-01-02")
	}
}

// NameColor returns the appropriate colour function for an entry name.
func ColorizeName(name string, isDir, isBucket, isAlias bool) string {
	switch {
	case isAlias:
		return Alias.Sprint(name)
	case isBucket:
		return Bucket.Sprint(name)
	case isDir:
		return Dir.Sprint(name)
	default:
		return Object.Sprint(name)
	}
}

// Pad pads s to width, accounting for ANSI escape sequences is the caller's
// responsibility; this is only used for plain strings.
func Pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
