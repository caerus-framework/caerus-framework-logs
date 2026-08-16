# caerus-framework-logs

[![CI](https://github.com/caerus-framework/caerus-framework-logs/actions/workflows/ci.yml/badge.svg)](https://github.com/caerus-framework/caerus-framework-logs/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/caerus-framework/caerus-framework-logs/graph/badge.svg)](https://codecov.io/gh/caerus-framework/caerus-framework-logs)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)


Caerus Framework — logs component.

A `log/slog`-based logging component that plugs into the
[`caerus-framework`](https://github.com/caerus-framework/caerus-framework) core
as the first bootstrap stage, so logging is available to every other
component's `Init`. Adds caller reporting (logrus-style) and optional full stack
tracebacks on errors — `log/slog` plus traceback.

## Features

- `log/slog` handlers: structured **text** or **JSON** output.
- **Caller reporting**: attach the calling file/line to every record
  (`WithReportCaller`), like logrus `ReportCaller`.
- **Stack tracebacks**: attach a formatted stack traceback to records at or
  above a configurable level (`WithStackTraces` + `WithStackLevel`), with the
  handler's own frames and slog/runtime internals filtered out.
- **Dynamic level**: process-global `SetLevel`, plus per-component `SetLevelFor`
  / `ResetLevel` so a noisy peer can be traced without flooding the process.
- **Runtime reconfiguration**: `Reconfigure` rebuilds the logger (format, writer,
  caller reporting, stack traces) and pushes the rebuilt logger to every
  `OnReconfigure` / `OnReconfigureFor` subscriber, so components keep a live
  logger without polling.
- Full `CaerusComponent` lifecycle; no `os.Exit`, no panics.

## Cooperative redaction

Logs **prints** secrets as `[redacted]`. Configuration **declares** which
fields are secrets (`secret:"redact"` on the config struct). This is
cooperative: `fmt.Sprintf`, error strings, and `slog.Info("cfg", cfg)` on a
raw struct still leak. There is no process-wide `ReplaceAttr` on
`slog.Default()`.

| Concern | What to do | Default |
|---|---|---|
| Passwords, API keys, DSN userinfo | `secret:"redact"` + `cf_logs.RedactedString` / `cf_configuration.LogArgs` | Print `[redacted]`; presence via `SecretSet("password", v)` → `password_set=true` |
| HTTP query, body, cookies | Stay off in RequestLog (http module) | Off |
| Client IP | `ClientIP(addr, mode)` with `full` / `partial` / `omit` | Caller chooses. Pass the address you already trust (`RemoteAddr` after your proxy policy). Do **not** pass `X-Forwarded-For` into this helper — it does not decide whether a header is forged. |

```go
log.Info("reload", "password", cf_logs.RedactedString(cfg.Password), "host", cfg.Host)
log.Info("reload", cf_logs.SecretSet("password", cfg.Password), "host", cfg.Host)
log.Info("reload", cf_configuration.LogArgs(cfg)...) // honors secret tags; overlay/Get unchanged
```

`ReplaceAttrSecretKeys("password")` is an **opt-in** handler hook for keys you
list. It does not walk structs.

`RedactURLUserinfo` strips a URL password for error strings. Prefer not
wrapping `pgx`/`url.Parse` errors that interpolate the raw DSN.

## Wiring

Two wiring shapes are supported. Prefer the **golden** path: seed logs through
`cf.FrameworkOptions.Logs` so core registers the component and binds its config
source. Use bare `AddComponent` only for one-off binaries or tests.

### Golden path (`FrameworkOptions.Logs`)

`cf.New` always builds logs as the first bootstrap stage. Point it at the
`logs` configuration source (default file `config/logs.json`, env `LOGS_`)
with the seed’s `ConfigSource` field:

```go
fw := cf.New(&cf.FrameworkOptions{
	Logs: &cf.LogsSettings{
		Format:       "json",
		Level:        "info",
		ConfigSource: "logs", // Source.Name; Owner is cf_logs.ComponentName
		// Optional forensics (same fields as LogConfig; *bool omit = default):
		// ReportCaller: ptr(true), StackTraces: ptr(true), StackLevel: "error",
	},
	Observability: &cf.ObservabilitySettings{Bind: ":9090", ConfigSource: "observability"},
	Components: []cf.CaerusComponent{
		// chassis + app class …
	},
})
if err := fw.RunWithSignals(context.Background()); err != nil {
	log.Fatal(err)
}
```

Import `_ "github.com/caerus-framework/caerus-framework-logs"` (or any
`cf_logs` symbol) so the core factory registers. Peers subscribe in `Init`
with `OnReconfigureFor(c.Name(), …)` and list `cf_logs.ComponentName` in
`GetDependencies`:

```go
func (c *CFPostgres) GetDependencies() []string {
	return []string{cf_logs.ComponentName}
}

func (c *CFPostgres) Init(ctx context.Context, fw *cf.CaerusFramework) error {
	if !c.loggerSet {
		if logs, ok := cf.Get[*cf_logs.Logs](fw); ok {
			c.logsSub = logs.OnReconfigureFor(c.Name(), func(l *slog.Logger) { c.log = l })
		}
	}
	c.log.Info("initializing postgresql component")
	return nil
}

func (c *CFPostgres) Shutdown(ctx context.Context) error {
	if c.logsSub != nil {
		c.logsSub.Unsubscribe()
		c.logsSub = nil
	}
	// …
	return nil
}
```

### Simple path (`AddComponent`)

For a minimal binary that builds logs by hand:

```go
fw := cf.New()
logsComp := cf_logs.New(
	cf_logs.WithWriter(os.Stdout),
	cf_logs.WithFormat(cf_logs.FormatJSON),
	cf_logs.WithLevel(slog.LevelInfo),
	cf_logs.WithReportCaller(true),
	cf_logs.WithStackTraces(true), // traceback on slog.LevelError and above
	cf_logs.WithConfigSource("logs"),
)
_ = fw.AddComponent(logsComp)
// … register the rest, then Run / RunWithSignals
```

### Configuration

Options are construction-time `cf_logs.Option`s:

| Option | Default | Purpose |
|---|---|---|
| `WithLevel(slog.Level)` | `slog.LevelInfo` | Process-global minimum (also `SetLevel`; overrides via `SetLevelFor`). |
| `WithFormat(Format)` | `FormatText` | `FormatText` or `FormatJSON`. |
| `WithWriter(io.Writer)` | `os.Stdout` | Output destination. |
| `WithReportCaller(bool)` | `false` | Add `source` (file:line) to every record. |
| `WithStackTraces(bool)` | `false` | Attach a stack traceback to records at/above the stack level. |
| `WithStackLevel(slog.Level)` | `slog.LevelError` | Threshold for stack tracebacks. |
| `WithConfigSource(string)` | `""` | Bind a `Source[LogConfig]` (owner `cf_logs.ComponentName`); `OnConfigReload` applies its value live via `ApplyConfig`. |

`cf.LogsSettings` (golden seed) mirrors `LogConfig`: `Format`, `Level`,
`ReportCaller` / `StackTraces` (`*bool`), `StackLevel` (string), and
`ConfigSource`. File/env reload still wins after Init.

Level and format names are parseable for config-driven setup via
`ParseFormat` (json/text) and `ParseLevel` (debug/info/warn|warning/error).
Both are **case-insensitive**; invalid values fail parse (format) or fall
back to `LevelInfo` with an error (level). `stack_level` uses the same
level names.

### Config-driven (`LogConfig` / `WithConfigSource`)

As a core component, logs takes its option values from the configuration
component: register a `cf_configuration.Source[cf_logs.LogConfig]` owned by
`cf_logs.ComponentName` (`config/logs.json` in the demoapp). The logs component
is notified **once at `Init`** with the source's value and again on every change
(`ApplyConfig`): format and explicit caller/stack-trace flags rebuild the logger
via `Reconfigure`; omitted `report_caller` / `stack_traces` keep the current
values (`*bool` — omit ≠ false). Level goes through `SetLevel` so
`SetLevelFor(component, …)` overrides keep working. Invalid values are logged
and skipped (last-good kept).

```json
{ "format": "json", "level": "info", "report_caller": true, "stack_traces": false, "stack_level": "error" }
```

`stack_level` is the threshold for tracebacks when `stack_traces` is on
(same names as `level`; empty keeps the current threshold, default error).

### Runtime reconfiguration

`Logs.Reconfigure(opts ...Option)` rebuilds the logger from the given
handler-affecting options (`WithFormat`, `WithWriter`, `WithReportCaller`,
`WithStackTraces`, `WithStackLevel`) and delivers the new logger to every
subscriber. `WithLevel` is **not** applied by `Reconfigure` — the level is
managed exclusively via `SetLevel`, and a rebuild preserves the current runtime
level.

```go
logs.Reconfigure(cf_logs.WithFormat(cf_logs.FormatJSON), cf_logs.WithReportCaller(true))
```

Framework components register with `OnReconfigureFor(name, fn)` (pass `Name()`,
including `WithName` aliases) and receive a level-filtered logger immediately,
then again on every `Reconfigure`. App code that wants the process-global logger
can use `OnReconfigure(fn)` or `Logger()`. The returned `*cf_logs.Subscription`
must be `Unsubscribe`d on `Shutdown`:

```go
sub := logs.OnReconfigureFor(c.Name(), func(l *slog.Logger) { c.log = l })
defer sub.Unsubscribe()
```

### Per-component levels

```go
logs.SetLevel(slog.LevelInfo)                 // process default
logs.SetLevelFor("vpq", slog.LevelDebug)      // only vpq (and WithName aliases)
logs.ResetLevel("vpq")                        // follow global again
_ = logs.LevelFor("vpq")                      // effective minimum for that name
```

`SetLevel` / `SetLevelFor` deliberately do **not** notify subscribers: the logger
pointer is unchanged, and each holder's `Leveler` observes the change
immediately. A component override may be **noisier or quieter** than the global
level. `Logs.Shutdown` drops all remaining subscribers, so no deliveries happen
during teardown.

Reloadable map (same names as `level`):

```json
{
  "format": "json",
  "level": "info",
  "component_levels": { "interest": "debug" }
}
```

Keys are component `Name()` values (`WithName("interest")` → `"interest"`, not
the default `"vpq"`). Applying a map **replaces** overrides: a name missing from
the new map follows the process-global level again. Omit the field to keep
last-good; `"component_levels": {}` clears all overrides. Invalid entries log
Error and skip that key.

## Component contract

Implements `caerusframework.CaerusComponent`:

- `Name()` → `"logs"` (`cf_logs.ComponentName`)
- `GetInitOrderStage()` → `caerusframework.LogsStage` (first bootstrap stage)
- `Init` → no-op (logger already built at construction; peers subscribe later)
- `Shutdown` → clears `OnReconfigure` / `OnReconfigureFor` subscribers so they
  stop receiving rebuilt loggers during teardown (the writer is still the
  caller's concern)

Does **not** implement `MetricsProvider`. Bootstrap logs cannot import
`caerus-framework-observability` (cycle). When both are registered,
observability’s private `logsMetricsCollector` scrapes this component and
emits `logs_info` (format, global level, report_caller, stack traces, stack
level) plus one `logs_component_level` sample per `SetLevelFor` override on
every `/metrics` scrape.

## Docs

- [ARCHITECTURE.md](https://github.com/caerus-framework/caerus-framework/blob/main/docs/ARCHITECTURE.md)
  — component model and stage ordering.
- [LIFECYCLE.md](https://github.com/caerus-framework/caerus-framework/blob/main/docs/LIFECYCLE.md)
  — lifecycle guarantees.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
