package ui

import (
	"io"
	"os"
	"sync"

	"github.com/mattn/go-isatty"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// progressDisabled suppresses progress bars regardless of the terminal. It is
// set for machine-oriented output such as --json.
var (
	progressMu       sync.Mutex
	progressDisabled bool
)

// DisableProgress turns progress bars off for the whole process. The default is
// to show them only when stderr is an interactive terminal.
func DisableProgress() {
	progressMu.Lock()
	progressDisabled = true
	progressMu.Unlock()
}

func progressEnabled() bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	if progressDisabled {
		return false
	}
	return isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
}

// Progress is a small wrapper around mpb that renders transfer bars on stderr.
type Progress struct {
	*mpb.Progress
	mu     sync.Mutex
	writer io.Writer
}

// NewProgress creates a progress container. When stderr is not a terminal (or
// progress was explicitly disabled) it renders nowhere, so piped or captured
// output stays clean while transfers still work.
func NewProgress() *Progress {
	w := io.Writer(os.Stderr)
	if !progressEnabled() {
		w = io.Discard
	}
	return &Progress{
		Progress: mpb.New(mpb.WithOutput(w), mpb.WithWidth(48)),
		writer:   w,
	}
}

// AddBar registers a new progress bar for a named transfer. initial > 0 is
// used when resuming.
func (p *Progress) AddBar(name string, total, initial int64) *mpb.Bar {
	return p.Progress.AddBar(total,
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name(truncate(name, 28), decor.WCSyncSpaceR),
			decor.CountersKibiByte(" % .1f / % .1f", decor.WCSyncSpaceR),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WC{W: 5}),
			decor.AverageSpeed(decor.SizeB1024(0), " % .1f/s", decor.WCSyncSpaceR),
		),
	)
}

// Wait blocks until all bars are complete.
func (p *Progress) Wait() { p.Progress.Wait() }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
