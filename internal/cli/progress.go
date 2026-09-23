package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
)

// progress renders fetch events. On a terminal it keeps one status line up
// to date; otherwise it prints a line per finished file.
type progress struct {
	w     io.Writer
	tty   bool
	label string

	mu       sync.Mutex
	files    map[string]fileState
	finished int
	start    time.Time
	lastDraw time.Time
	drawn    bool
}

type fileState struct {
	done, total int64
	base        int64 // bytes already present when this run started
}

func newProgress(w io.Writer, label string) *progress {
	tty := false
	if f, ok := w.(*os.File); ok {
		tty = term.IsTerminal(int(f.Fd()))
	}
	return &progress{w: w, tty: tty, label: label, files: map[string]fileState{}, start: time.Now()}
}

func (p *progress) event(e fetch.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fs := p.files[e.Path]
	switch e.State {
	case fetch.Started:
		fs = fileState{done: e.Done, total: e.Total, base: e.Done}
	case fetch.Progress:
		fs.done, fs.total = e.Done, e.Total
	case fetch.Done:
		fs.done, fs.total = e.Total, e.Total
		p.finished++
		if !p.tty {
			fmt.Fprintf(p.w, "  %s (%s)\n", e.Path, humanBytes(e.Total))
		}
	case fetch.Retrying:
		p.clearLine()
		fmt.Fprintf(p.w, "  %s: %v; retrying\n", e.Path, e.Err)
	case fetch.Failed, fetch.Skipped:
		return
	}
	p.files[e.Path] = fs
	if p.tty && time.Since(p.lastDraw) > 150*time.Millisecond {
		p.draw()
	}
}

func (p *progress) draw() {
	var done, total, fresh int64
	for _, f := range p.files {
		done += f.done
		total += f.total
		fresh += f.done - f.base
	}
	rate := float64(fresh) / time.Since(p.start).Seconds()
	fmt.Fprintf(p.w, "\r\033[K%s  %d/%d files  %s / %s  %s/s",
		p.label, p.finished, len(p.files), humanBytes(done), humanBytes(total), humanBytes(int64(rate)))
	p.lastDraw = time.Now()
	p.drawn = true
}

func (p *progress) clearLine() {
	if p.tty && p.drawn {
		fmt.Fprint(p.w, "\r\033[K")
		p.drawn = false
	}
}

// finish removes the status line so the summary starts on a clean line.
func (p *progress) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLine()
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
