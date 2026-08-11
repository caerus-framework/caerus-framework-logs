package cf_logs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	cf "github.com/caerus-framework/caerus-framework"
)

// ComponentName is the framework component name for the logs component. It is
// the identifier other components use in GetDependencies to require logging.
const ComponentName = "logs"

// Format selects the slog handler output format.
type Format int

const (
	// FormatText emits human-readable key/value lines (slog's TextHandler).
	FormatText Format = iota
	// FormatJSON emits structured JSON lines (slog's JSONHandler).
	FormatJSON
)

// String returns the canonical name of the format.
func (f Format) String() string {
	switch f {
	case FormatJSON:
		return "json"
	default:
		return "text"
	}
}

// ParseFormat converts a canonical format name ("json", "text") into a Format.
// It returns an error for unknown names.
func ParseFormat(name string) (Format, error) {
	switch name {
	case "json":
		return FormatJSON, nil
	case "text":
		return FormatText, nil
	default:
		return FormatText, fmt.Errorf("cf_logs: unknown log format %q", name)
	}
}

// Logs is the caerus-framework-logs component. It wraps a *slog.Logger and is
// registered with the framework as the "logs" component so that every other
// component can retrieve it via cf.Get[*cf_logs.Logs] or depend on it by name.
//
// The logger is built at construction and can be rebuilt at runtime with
// Reconfigure. SetLevel changes the process-global minimum; SetLevelFor sets a
// per-component override used by OnReconfigureFor subscribers. Level changes
// do not rebuild the logger.
type Logs struct {
	mu           sync.RWMutex
	logger       *slog.Logger // process-global filtered logger (Logger / OnReconfigure)
	base         slog.Handler // shared format/writer stack (no level filter)
	level        slog.LevelVar
	overrides    map[string]slog.Level // per-component minima; absent → global
	cfg          options
	fw           *cf.CaerusFramework
	configSource string // configuration source name for OnConfigReload ("" = none)
	subs         []sub
	nextID       int
}

// sub is one registered reconfiguration subscriber. IDs let Unsubscribe remove
// a specific subscriber without comparing function values. name, when non-empty,
// selects the per-component level override for delivered loggers.
type sub struct {
	id   int
	name string
	fn   func(*slog.Logger)
}

type options struct {
	level        slog.Level
	format       Format
	writer       io.Writer
	reportCaller bool
	stackTraces  bool
	stackLevel   slog.Level
	configSource string
}

// Option configures the logs component at construction time.
type Option func(*options)

// WithLevel sets the process-global minimum level that is emitted (default
// slog.LevelInfo). The level can still be changed at runtime with Logs.SetLevel.
// Reconfigure does not apply WithLevel; the global level is always managed via
// SetLevel. Per-component overrides use SetLevelFor.
func WithLevel(level slog.Level) Option {
	return func(o *options) { o.level = level }
}

// WithFormat selects the output format, text or JSON (default FormatText).
func WithFormat(format Format) Option {
	return func(o *options) { o.format = format }
}

// WithWriter sets the output destination (default os.Stdout).
func WithWriter(w io.Writer) Option {
	return func(o *options) { o.writer = w }
}

// WithReportCaller enables the source (file:line) of the log call to be
// recorded on every record, like logrus's ReportCaller (default false).
func WithReportCaller(enabled bool) Option {
	return func(o *options) { o.reportCaller = enabled }
}

// WithStackTraces attaches a formatted stack traceback to every record at or
// above the stack level (default false).
func WithStackTraces(enabled bool) Option {
	return func(o *options) { o.stackTraces = enabled }
}

// WithStackLevel sets the threshold at or above which stack tracebacks are
// attached (default slog.LevelError). It only takes effect when stack traces
// are enabled.
func WithStackLevel(level slog.Level) Option {
	return func(o *options) { o.stackLevel = level }
}

// WithConfigSource names the configuration source (caerus-framework-
// configuration) whose LogConfig is applied to the component. The logs module
// cannot read the configuration component directly (import cycle), so the
// framework delivers the freshly loaded value through OnConfigReload. The
// component self-registers the source during argv absorption (default file
// config/<name>.json, env prefix LOGS_, owner cf_logs); an argv --<name>
// file-path override wins, and the app may also register its own Source[LogConfig]
// for a custom default. Until the source loads, construction-time defaults apply.
func WithConfigSource(name string) Option {
	return func(o *options) { o.configSource = name }
}

// New creates a logs component. Configure it with options; defaults are text
// format, Info level, os.Stdout, caller reporting off, stack tracebacks off.
func New(opts ...Option) *Logs {
	o := options{
		level:      slog.LevelInfo,
		format:     FormatText,
		writer:     os.Stdout,
		stackLevel: slog.LevelError,
	}
	for _, opt := range opts {
		opt(&o)
	}
	l := &Logs{cfg: o, configSource: o.configSource, overrides: make(map[string]slog.Level)}
	l.level.Set(o.level)
	l.buildLogger()
	return l
}

// buildLogger constructs the shared handler stack and the process-global
// filtered logger. Callers must hold l.mu when the component is shared.
func (l *Logs) buildLogger() {
	opts := &slog.HandlerOptions{
		Level:     alwaysLevel{},
		AddSource: l.cfg.reportCaller,
	}
	var handler slog.Handler
	if l.cfg.format == FormatJSON {
		handler = slog.NewJSONHandler(l.cfg.writer, opts)
	} else {
		handler = slog.NewTextHandler(l.cfg.writer, opts)
	}
	if l.cfg.stackTraces {
		handler = &stackTraceHandler{next: handler, level: l.cfg.stackLevel}
	}
	l.base = handler
	l.logger = l.wrapLocked("")
}

// wrapLocked returns a logger filtered by the process-global level (name == "")
// or by the named component's override (else global). Callers must hold l.mu
// (or be in New before sharing).
func (l *Logs) wrapLocked(name string) *slog.Logger {
	var leveler slog.Leveler = &l.level
	if name != "" {
		leveler = &componentLeveler{logs: l, name: name}
	}
	return slog.New(&levelFilterHandler{next: l.base, leveler: leveler})
}

// Name implements cf.CaerusComponent.
func (l *Logs) Name() string { return ComponentName }

// GetInitOrderStage implements cf.CaerusComponent. Logging is the very first
// bootstrap stage, so it is available to every other component's Init.
func (l *Logs) GetInitOrderStage() cf.Stage { return cf.LogsStage }

// Init implements cf.CaerusComponent. It is a no-op: the logger is fully
// configured at construction time.
func (l *Logs) Init(ctx context.Context, fw *cf.CaerusFramework) error {
	l.fw = fw
	return nil
}

// Shutdown implements cf.CaerusComponent. The writer is the caller's concern;
// there is nothing to release. Pending reconfiguration subscribers are dropped
// so they stop receiving deliveries during teardown.
func (l *Logs) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	l.subs = nil
	l.mu.Unlock()
	return nil
}

// Logger returns the process-global slog.Logger (filtered by SetLevel). Prefer
// OnReconfigureFor from framework components so they honor SetLevelFor.
func (l *Logs) Logger() *slog.Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.logger
}

// LoggerFor returns a logger filtered by the named component's level override
// when set, otherwise by the process-global SetLevel. The pointer is stable
// until the next Reconfigure.
func (l *Logs) LoggerFor(name string) *slog.Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if name == "" {
		return l.logger
	}
	return l.wrapLocked(name)
}

// Level returns the process-global minimum log level (SetLevel).
func (l *Logs) Level() slog.Level { return l.level.Level() }

// LevelFor returns the effective minimum level for name: the SetLevelFor
// override when present, otherwise the process-global level.
func (l *Logs) LevelFor(name string) slog.Level {
	if name == "" {
		return l.level.Level()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if lv, ok := l.overrides[name]; ok {
		return lv
	}
	return l.level.Level()
}

// SetLevel changes the process-global minimum log level at runtime. Components
// subscribed with OnReconfigureFor keep any SetLevelFor override; others and
// Logger() observe the new global immediately. SetLevel does not rebuild the
// logger, so reconfiguration subscribers are not notified.
func (l *Logs) SetLevel(level slog.Level) { l.level.Set(level) }

// SetLevelFor sets a per-component minimum log level. name should be the
// component's Name() (including WithName aliases). The override applies to
// LoggerFor and OnReconfigureFor subscribers for that name. It does not notify
// subscribers (the logger pointer is unchanged).
func (l *Logs) SetLevelFor(name string, level slog.Level) {
	if name == "" {
		l.SetLevel(level)
		return
	}
	l.mu.Lock()
	if l.overrides == nil {
		l.overrides = make(map[string]slog.Level)
	}
	l.overrides[name] = level
	l.mu.Unlock()
}

// ResetLevel drops the per-component override for name so it follows SetLevel
// again. No-op when name is empty or has no override.
func (l *Logs) ResetLevel(name string) {
	if name == "" {
		return
	}
	l.mu.Lock()
	delete(l.overrides, name)
	l.mu.Unlock()
}

// Format returns the configured output format (text or JSON).
func (l *Logs) Format() Format {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.format
}

// ReportCaller returns whether the logger includes caller information.
func (l *Logs) ReportCaller() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.reportCaller
}

// StackTraces returns whether the logger emits stack traces.
func (l *Logs) StackTraces() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.stackTraces
}

// StackLevel returns the level at which stack traces are emitted.
func (l *Logs) StackLevel() slog.Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.stackLevel
}

// Overrides returns a snapshot of per-component level overrides.
func (l *Logs) Overrides() map[string]slog.Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]slog.Level, len(l.overrides))
	for k, v := range l.overrides {
		out[k] = v
	}
	return out
}

// Reconfigure rebuilds the logger from the given construction options and
// delivers the new logger to every OnReconfigure / OnReconfigureFor subscriber.
// It applies the handler-affecting options — WithFormat, WithWriter,
// WithReportCaller, WithStackTraces, WithStackLevel. WithLevel is not applied
// here: the global level is managed exclusively through SetLevel, and rebuilding
// preserves the current runtime level and per-component overrides. Subscribers
// are notified outside the internal lock.
func (l *Logs) Reconfigure(opts ...Option) {
	l.mu.Lock()
	for _, opt := range opts {
		opt(&l.cfg)
	}
	l.buildLogger()
	type delivery struct {
		fn     func(*slog.Logger)
		logger *slog.Logger
	}
	deliveries := make([]delivery, 0, len(l.subs))
	for _, s := range l.subs {
		next := l.logger
		if s.name != "" {
			next = l.wrapLocked(s.name)
		}
		deliveries = append(deliveries, delivery{fn: s.fn, logger: next})
	}
	l.mu.Unlock()
	for _, d := range deliveries {
		d.fn(d.logger)
	}
}

// ApplyConfig applies a LogConfig to the running component. Non-empty Format
// and non-nil ReportCaller/StackTraces rebuild the logger (delivering the new
// logger to every OnReconfigure / OnReconfigureFor subscriber); omitted bool
// fields keep the current forensic knobs. Level is applied through SetLevel so
// per-component overrides (SetLevelFor) keep working. Invalid format/level
// values are logged and skipped (last-good).
func (l *Logs) ApplyConfig(cfg LogConfig) {
	opts := make([]Option, 0, 3)
	if cfg.Format != "" {
		f, err := ParseFormat(cfg.Format)
		if err != nil {
			l.Logger().Error("cf_logs: invalid format in log config; keeping previous", "format", cfg.Format, "err", err)
		} else {
			opts = append(opts, WithFormat(f))
		}
	}
	// *bool: omitted/nil keeps the current forensic knobs (do not treat omit as false).
	if cfg.ReportCaller != nil {
		opts = append(opts, WithReportCaller(*cfg.ReportCaller))
	}
	if cfg.StackTraces != nil {
		opts = append(opts, WithStackTraces(*cfg.StackTraces))
	}
	if len(opts) > 0 {
		l.Reconfigure(opts...)
	}
	if cfg.Level != "" {
		lv, err := ParseLevel(cfg.Level)
		if err != nil {
			l.Logger().Error("cf_logs: invalid level in log config; keeping previous", "level", cfg.Level, "err", err)
		} else {
			l.SetLevel(lv)
		}
	}
}

// OnConfigReload implements cf.ConfigReloader. It applies the freshly loaded
// LogConfig for the source named by WithConfigSource (see ApplyConfig). The
// configuration component delivers the value directly because the logs module
// cannot import it.
func (l *Logs) OnConfigReload(source string, cfg any) {
	if source != l.configSource {
		return
	}
	c, ok := cfg.(*LogConfig)
	if !ok {
		return
	}
	l.ApplyConfig(*c)
}

// OnReconfigure registers fn to receive the process-global logger immediately
// and again every time Reconfigure rebuilds it. Prefer OnReconfigureFor from
// framework components so SetLevelFor can isolate verbosity. SetLevel changes
// are deliberately not delivered, since the logger pointer is unchanged.
func (l *Logs) OnReconfigure(fn func(*slog.Logger)) *Subscription {
	return l.OnReconfigureFor("", fn)
}

// OnReconfigureFor is like OnReconfigure but the delivered logger honors
// SetLevelFor(name) when set, otherwise the process-global SetLevel. Pass the
// component's Name() (including WithName aliases). An empty name behaves like
// OnReconfigure.
func (l *Logs) OnReconfigureFor(name string, fn func(*slog.Logger)) *Subscription {
	l.mu.Lock()
	s := sub{id: l.nextID, name: name, fn: fn}
	l.nextID++
	l.subs = append(l.subs, s)
	current := l.logger
	if name != "" {
		current = l.wrapLocked(name)
	}
	l.mu.Unlock()
	fn(current)
	return &Subscription{logs: l, id: s.id}
}

func (l *Logs) unsubscribe(id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, s := range l.subs {
		if s.id == id {
			l.subs = append(l.subs[:i], l.subs[i+1:]...)
			return
		}
	}
}

// Subscription is the handle returned by OnReconfigure. Unsubscribe removes the
// registered callback so it stops receiving rebuilt loggers. It is idempotent.
type Subscription struct {
	logs *Logs
	id   int
}

// Unsubscribe stops the registered callback from receiving further deliveries.
func (s *Subscription) Unsubscribe() {
	if s != nil && s.logs != nil {
		s.logs.unsubscribe(s.id)
	}
}

var _ cf.CaerusComponent = (*Logs)(nil)
var _ cf.ConfigReloader = (*Logs)(nil)
