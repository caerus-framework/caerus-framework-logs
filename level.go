package cf_logs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// ParseLevel converts a canonical level name ("debug", "info", "warn" or
// "error") into a slog.Level. It returns an error for unknown names.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("cf_logs: unknown log level %q", name)
	}
}

// alwaysLevel is a Leveler that never filters: per-logger levelFilterHandlers
// own the effective minimum so a component can be quieter or noisier than the
// process-global SetLevel.
type alwaysLevel struct{}

func (alwaysLevel) Level() slog.Level { return slog.Level(-1 << 20) }

// componentLeveler returns a named override when set, otherwise the global
// LevelVar. Level() is safe for concurrent use with SetLevel / SetLevelFor.
type componentLeveler struct {
	logs *Logs
	name string
}

func (c *componentLeveler) Level() slog.Level {
	c.logs.mu.RLock()
	defer c.logs.mu.RUnlock()
	if lv, ok := c.logs.overrides[c.name]; ok {
		return lv
	}
	return c.logs.level.Level()
}

// levelFilterHandler applies a Leveler on top of another handler. Used to give
// each OnReconfigureFor subscriber its own minimum level without rebuilding
// the shared format/writer stack when only a level changes.
type levelFilterHandler struct {
	next    slog.Handler
	leveler slog.Leveler
}

func (h *levelFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.leveler.Level() && h.next.Enabled(ctx, level)
}

func (h *levelFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h *levelFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelFilterHandler{next: h.next.WithAttrs(attrs), leveler: h.leveler}
}

func (h *levelFilterHandler) WithGroup(name string) slog.Handler {
	return &levelFilterHandler{next: h.next.WithGroup(name), leveler: h.leveler}
}
