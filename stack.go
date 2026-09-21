package cf_logs

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
)

// maxStackDepth bounds the number of frames captured per traceback.
const maxStackDepth = 64

// stackPathSegments is how many trailing slash-separated path parts
// WithTrimStackPaths keeps (module/dir/file.go).
const stackPathSegments = 3

// stackTraceHandler wraps another slog.Handler and attaches a formatted stack
// traceback as a "stack" attribute to every record at or above its threshold
// level. The traceback shows the application call path that produced the log
// call (the handler's own frames and slog/runtime internals are skipped).
type stackTraceHandler struct {
	next  slog.Handler
	level slog.Level
	trim  bool
}

func (h *stackTraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *stackTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= h.level {
		r.AddAttrs(slog.String("stack", stackTrace(h.trim)))
	}
	return h.next.Handle(ctx, r)
}

func (h *stackTraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &stackTraceHandler{next: h.next.WithAttrs(attrs), level: h.level, trim: h.trim}
}

func (h *stackTraceHandler) WithGroup(name string) slog.Handler {
	return &stackTraceHandler{next: h.next.WithGroup(name), level: h.level, trim: h.trim}
}

// stackTrace returns a formatted traceback of the calling goroutine. Frames
// belonging to this package (the handler machinery), to log/slog, and to the
// runtime are skipped, so the trace begins at the application code that called
// the logger. Each frame is function name, file path, and line — not
// arguments or locals. When trim is true, file paths keep only the last
// stackPathSegments slash-separated parts.
func stackTrace(trim bool) string {
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
		file := f.File
		if trim {
			file = trimStackPath(file)
		}
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", name, file, f.Line)
	}
	return b.String()
}

func trimStackPath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	if len(parts) <= stackPathSegments {
		return path
	}
	return strings.Join(parts[len(parts)-stackPathSegments:], "/")
}
