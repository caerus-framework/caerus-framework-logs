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

## Usage

Complete wiring:

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    caerusframework "github.com/caerus-framework/caerus-framework"
    cf_logs "github.com/caerus-framework/caerus-framework-logs"
)

func main() {
    fw := caerusframework.New() // bootstrap stages pre-registered; LogsStage first

    logsComp := cf_logs.New(
        cf_logs.WithWriter(os.Stdout),
        cf_logs.WithFormat(cf_logs.FormatJSON),
        cf_logs.WithLevel(slog.LevelInfo),
        cf_logs.WithReportCaller(true),
        cf_logs.WithStackTraces(true), // traceback on slog.LevelError and above
    )
    if err := fw.AddComponent(logsComp); err != nil { /* ... */ }

    // ... register the rest of the components ...

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    if err := fw.Run(ctx); err != nil { /* app failed */ }
}
```

From another component's `Init` (logging initializes first, so it is always
available). Prefer `OnReconfigureFor(c.Name(), …)` over `Logger()`: it delivers
a logger that honors `SetLevelFor` for that name, immediately and again whenever
the logger is rebuilt:

```go
func (c *CFPostgres) Init(ctx context.Context, fw *cf.CaerusFramework) error {
    if logs, ok := caerusframework.Get[*cf_logs.Logs](fw); ok {
        c.logsSub = logs.OnReconfigureFor(c.Name(), func(l *slog.Logger) { c.log = l })
    }
    c.log.Info("initializing postgresql component")
    return nil
}

func (c *CFPostgres) Shutdown(ctx context.Context) error {
    if c.logsSub != nil {
        c.logsSub.Unsubscribe()
        c.logsSub = nil
    }
    // ...
}
```

Other components may also depend on it by name in `GetDependencies`:

```go
func (c *CFPostgres) GetDependencies() []string { return []string{cf_logs.ComponentName} }
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

Level and format names are parseable for config-driven setup via
`ParseFormat` (json/text) and `ParseLevel` (debug/info/warn|warning/error,
case-insensitive; invalid values fall back to `LevelInfo`).

### Config-driven (`LogConfig` / `WithConfigSource`)

As a core component, logs takes its option values from the configuration
component: register a `cf_configuration.Source[cf_logs.LogConfig]` owned by
`cf_logs.ComponentName` (`config/logs.json` in the demoapp). The logs component
is notified **once at `Init`** with the source's value and again on every change
(`ApplyConfig`): format/caller/stack-trace flags rebuild the logger via
`Reconfigure`; the level goes through `SetLevel` so `SetLevelFor(component, …)`
overrides keep working. Invalid values are logged and skipped (last-good kept).

```json
{ "format": "json", "level": "info", "report_caller": false, "stack_traces": false }
```

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

## Component contract

Implements `caerusframework.CaerusComponent`:

- `Name()` → `"logs"` (`cf_logs.ComponentName`)
- `GetInitOrderStage()` → `caerusframework.LogsStage` (first bootstrap stage)
- `Init` / `Shutdown` → no-ops (nothing to allocate or release)
- Implements `cf.MetricsProvider`: contributes `logs_info` (format, global
  level, report_caller, stack traces) and one `logs_component_level`
  sample per `SetLevelFor` override, live on every scrape.

## Docs

- [ARCHITECTURE.md](https://github.com/caerus-framework/caerus-framework/blob/main/docs/ARCHITECTURE.md)
  — component model and stage ordering.
- [LIFECYCLE.md](https://github.com/caerus-framework/caerus-framework/blob/main/docs/LIFECYCLE.md)
  — lifecycle guarantees.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
