// Package cliui contains the small terminal rendering helpers shared by the
// fvc command. Keeping this package free of command parsing makes CLI output
// easier to test and keeps command handlers focused on daemon calls.
package cliui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lucaspose/fvc/proto"
)

const (
	ColorReset = "\033[0m"
	ColorBold  = "\033[1m"
	ColorDim   = "\033[2m"
	ColorRed   = "\033[31m"
	ColorGreen = "\033[32m"
	ColorCyan  = "\033[36m"
	ColorGray  = "\033[90m"
)

type ProgressLine struct {
	mu      sync.Mutex
	active  bool
	label   string
	message string
	current int64
	total   int64
	done    chan struct{}
}

func PrintStep(label, message string) {
	fmt.Printf("%s %s\n", ColorLabel(label, ColorCyan), message)
}

func PrintSuccess(message string) {
	fmt.Printf("%s %s\n", ColorLabel("OK", ColorGreen), message)
}

func PrintFailure(message string) {
	fmt.Printf("%s %s\n", ColorLabel("ERR", ColorRed), message)
}

func PrintKV(key, value string) {
	fmt.Printf("  %s%-8s%s %s\n", Color(ColorDim), key+":", Color(ColorReset), value)
}

func PrintRunEvent(progress *ProgressLine, event *proto.RunEvent) {
	switch event.GetStatus() {
	case "complete":
		if event.GetStage() == "done" {
			progress.Finish()
			return
		}
		progress.Finish()
		PrintSuccess(event.GetMessage())
	case "error":
		progress.Finish()
		PrintFailure(event.GetMessage())
	default:
		progress.Start(strings.ToUpper(event.GetStage()), event.GetMessage(), event.GetCurrent(), event.GetTotal())
	}
}

func PrintOperationEvent(progress *ProgressLine, event *proto.OperationEvent) {
	switch event.GetStatus() {
	case "complete":
		if event.GetStage() == "done" {
			progress.Finish()
			PrintSuccess(event.GetMessage())
			return
		}
		progress.Finish()
		PrintSuccess(event.GetMessage())
	case "error":
		progress.Finish()
		PrintFailure(event.GetMessage())
	default:
		progress.Start(strings.ToUpper(event.GetStage()), event.GetMessage(), event.GetCurrent(), event.GetTotal())
	}
}

func (p *ProgressLine) Start(label, message string, current, total int64) {
	p.mu.Lock()
	if p.active && p.label == label {
		p.message = message
		p.current = current
		p.total = total
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	p.Finish()
	p.mu.Lock()
	p.active = true
	p.label = label
	p.message = message
	p.current = current
	p.total = total
	p.done = make(chan struct{})
	done := p.done
	p.mu.Unlock()
	go func(done <-chan struct{}) {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("\r%s", p.render(i))
				i++
			}
		}
	}(done)
	fmt.Printf("%s", p.render(0))
}

func (p *ProgressLine) Finish() {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return
	}
	done := p.done
	p.active = false
	p.done = nil
	p.mu.Unlock()
	close(done)
	fmt.Print("\r\033[2K")
}

func (p *ProgressLine) render(frame int) string {
	p.mu.Lock()
	label := p.label
	message := p.message
	current := p.current
	total := p.total
	p.mu.Unlock()
	if total > 0 {
		return fmt.Sprintf("%s %s %s", ColorLabel(label, ColorCyan), message, ProgressBar(current, total))
	}
	return fmt.Sprintf("%s %s %s", ColorLabel(label, ColorCyan), message, IndeterminateBar(frame, current))
}

func ProgressBar(current, total int64) string {
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	const width = 28
	filled := 0
	percent := 0.0
	if total > 0 {
		percent = float64(current) / float64(total) * 100
		filled = int(float64(width) * float64(current) / float64(total))
	}
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	return fmt.Sprintf("%s[%s]%s %5.1f%% %s/%s", Color(ColorGray), bar, Color(ColorReset), percent, FormatBytes(current), FormatBytes(total))
}

func IndeterminateBar(frame int, current int64) string {
	const width = 28
	const block = 8
	if frame < 0 {
		frame = 0
	}
	span := width + block
	pos := frame % span
	start := pos - block
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i >= start && i < start+block {
			b.WriteByte('#')
		} else {
			b.WriteByte('-')
		}
	}
	if current > 0 {
		return fmt.Sprintf("%s[%s]%s %s", Color(ColorGray), b.String(), Color(ColorReset), FormatBytes(current))
	}
	return fmt.Sprintf("%s[%s]%s", Color(ColorGray), b.String(), Color(ColorReset))
}

func ColorLabel(label, code string) string {
	return fmt.Sprintf("%s[%s]%s", Color(code), label, Color(ColorReset))
}

func Color(code string) string {
	if os.Getenv("NO_COLOR") != "" {
		return ""
	}
	return code
}

func FormatMemoryUsage(usedBytes int64, limitMb int32) string {
	return fmt.Sprintf("%s / %dMB", FormatBytes(usedBytes), limitMb)
}

func FormatDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%ds", int64(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int64(d.Minutes()), int64(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int64(d.Hours()), int64(d.Minutes())%60)
}

func FormatExitCode(exitCode int32) string {
	if exitCode < 0 {
		return "-"
	}
	return fmt.Sprintf("%d", exitCode)
}

func FormatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1fPB", value/unit)
}
