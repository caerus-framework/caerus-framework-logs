package cf_logs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
)

// maxStackDepth bounds the number of frames captured per traceback.
const maxStackDepth = 64

// stackTraceHandler wraps another slog.Handler and attaches a formatted stack
// traceback as a "stack" attribute to every record at or above its threshold
// level. The traceback shows the application call path that produced the log
// call (the handler's own frames and slog/runtime internals are skipped).
type stackTraceHandler struct {
	next  slog.Handler
	level slog.Level
}

func (h *stackTraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *stackTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= h.level {
		r.AddAttrs(slog.String("stack", stackTrace()))
	}
	return h.next.Handle(ctx, r)
}

func (h *stackTraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &stackTraceHandler{next: h.next.WithAttrs(attrs), level: h.level}
}

func (h *stackTraceHandler) WithGroup(name string) slog.Handler {
	return &stackTraceHandler{next: h.next.WithGroup(name), level: h.level}
}

// stackTrace returns a formatted traceback of the calling goroutine. Frames
// belonging to this package (the handler machinery), to log/slog, and to the
// runtime are skipped, so the trace begins at the application code that called
// the logger.
func stackTrace() string {
	pcs := make([]uintptr, maxStackDepth)
	n := runtime.Callers(0, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	started := false
	for f, more := frames.Next(); more; f, more = frames.Next() {
		name := f.Function
		if !started {
			// Skip everything up to and including the handler machinery.
			if strings.Contains(name, "stackTraceHandler") || name == "cf_logs.stackTrace" {
				started = true
			}
			continue
		}
		if name == "" || strings.HasPrefix(name, "runtime.") || strings.HasPrefix(name, "log/slog.") {
			continue
		}
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", name, f.File, f.Line)
	}
	return b.String()
}
