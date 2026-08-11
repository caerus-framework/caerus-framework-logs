package cf_logs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	cf "github.com/caerus-framework/caerus-framework"
)

// traceEmit is a small indirection so tests can assert the stack traceback
// contains a stable, non-testing function name.
func traceEmit(l *Logs) {
	l.Logger().Error("boom")
}

func TestDefaultTextOutput(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf))

	l.Logger().Info("hello", "k", "v")

	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Fatalf("expected INFO level marker, got %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "k=v") {
		t.Fatalf("expected message and attribute, got %q", out)
	}
	if strings.Contains(out, "stack=") {
		t.Fatalf("stack traces must be off by default, got %q", out)
	}
	if strings.Contains(out, "source=") {
		t.Fatalf("caller reporting must be off by default, got %q", out)
	}
}

func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithFormat(FormatJSON))

	l.Logger().Info("hello", "k", "v")

	out := buf.String()
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("expected JSON object, got %q", out)
	}
	for _, want := range []string{`"level"`, `"msg"`, `"hello"`, `"k"`, `"v"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %s in JSON output, got %q", want, out)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Format
	}{
		{"json", FormatJSON},
		{"text", FormatText},
	} {
		got, err := ParseFormat(tc.name)
		if err != nil {
			t.Fatalf("ParseFormat(%q): %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("ParseFormat(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelWarn))

	l.Logger().Debug("debug message")
	l.Logger().Warn("warn message")

	out := buf.String()
	if strings.Contains(out, "debug message") {
		t.Fatalf("debug must be filtered at warn level, got %q", out)
	}
	if !strings.Contains(out, "warn message") {
		t.Fatalf("warn must be emitted at warn level, got %q", out)
	}
}

func TestSetLevelRuntime(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelInfo))
	if l.Level() != slog.LevelInfo {
		t.Fatalf("Level() = %v, want Info", l.Level())
	}

	l.Logger().Debug("skipped")
	l.SetLevel(slog.LevelDebug)
	l.Logger().Debug("now shown")

	out := buf.String()
	if strings.Contains(out, "skipped") {
		t.Fatalf("debug before SetLevel must be filtered, got %q", out)
	}
	if !strings.Contains(out, "now shown") {
		t.Fatalf("debug after SetLevel must be emitted, got %q", out)
	}
}

func TestReportCaller(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithReportCaller(true))

	l.Logger().Info("with caller")

	out := buf.String()
	if !strings.Contains(out, "source=") {
		t.Fatalf("expected source attribute, got %q", out)
	}
	if !strings.Contains(out, "logs_test.go") {
		t.Fatalf("expected caller to point at the test file, got %q", out)
	}
}

func TestStackTraces(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithStackTraces(true))

	traceEmit(l)

	out := buf.String()
	if !strings.Contains(out, "stack=") {
		t.Fatalf("expected stack attribute, got %q", out)
	}
	if !strings.Contains(out, "traceEmit") {
		t.Fatalf("expected stack traceback to include traceEmit, got %q", out)
	}
	if strings.Contains(out, "stackTraceHandler") {
		t.Fatalf("stack traceback must skip handler internals, got %q", out)
	}
}

func TestStackLevelThreshold(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithStackTraces(true), WithStackLevel(slog.LevelError))

	l.Logger().Warn("warning, no trace")
	warnOut := buf.String()
	if strings.Contains(warnOut, "stack=") {
		t.Fatalf("warn must not carry a stack trace below the threshold, got %q", warnOut)
	}

	buf.Reset()
	traceEmit(l)
	errOut := buf.String()
	if !strings.Contains(errOut, "traceEmit") {
		t.Fatalf("error must carry a stack trace, got %q", errOut)
	}
}

func TestOnReconfigureDeliversCurrentAndRebuilt(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf))

	var got []*slog.Logger
	sub := l.OnReconfigure(func(nl *slog.Logger) { got = append(got, nl) })
	defer sub.Unsubscribe()

	if len(got) != 1 || got[0] != l.Logger() {
		t.Fatalf("OnReconfigure must deliver the current logger immediately, got %d deliveries", len(got))
	}

	var buf2 bytes.Buffer
	l.Reconfigure(WithWriter(&buf2))
	if len(got) != 2 {
		t.Fatalf("expected a delivery on Reconfigure, got %d deliveries", len(got))
	}
	if got[1] == got[0] {
		t.Fatal("Reconfigure must deliver a rebuilt logger")
	}

	got[1].Info("after")
	if buf.String() != "" || !strings.Contains(buf2.String(), "after") {
		t.Fatal("rebuilt logger must write to the new writer")
	}
}

func TestSetLevelDoesNotRedeliver(t *testing.T) {
	l := New(WithWriter(&bytes.Buffer{}))

	var deliveries int
	sub := l.OnReconfigure(func(*slog.Logger) { deliveries++ })
	defer sub.Unsubscribe()
	if deliveries != 1 {
		t.Fatalf("expected immediate delivery, got %d", deliveries)
	}

	l.SetLevel(slog.LevelDebug)
	if deliveries != 1 {
		t.Fatalf("SetLevel must not redeliver the logger, got %d", deliveries)
	}
}

func TestSubscriptionUnsubscribeStopsDelivery(t *testing.T) {
	l := New(WithWriter(&bytes.Buffer{}))

	var deliveries int
	sub := l.OnReconfigure(func(*slog.Logger) { deliveries++ })
	if deliveries != 1 {
		t.Fatalf("expected immediate delivery, got %d", deliveries)
	}

	l.Reconfigure(WithFormat(FormatJSON))
	if deliveries != 2 {
		t.Fatalf("expected delivery on reconfigure, got %d", deliveries)
	}

	sub.Unsubscribe()
	l.Reconfigure(WithFormat(FormatText))
	if deliveries != 2 {
		t.Fatalf("unsubscribed subscriber must not be called, got %d", deliveries)
	}
}

func TestReconfigureSwitchesFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithFormat(FormatText))

	l.Logger().Info("plain")
	if !strings.HasPrefix(buf.String(), "time=") {
		t.Fatalf("expected text output, got %q", buf.String())
	}

	buf.Reset()
	l.Reconfigure(WithFormat(FormatJSON))
	l.Logger().Info("structured")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Fatalf("expected JSON output after Reconfigure, got %q", buf.String())
	}
}

func TestReconfigurePreservesRuntimeLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelInfo))

	l.SetLevel(slog.LevelDebug)
	l.Reconfigure(WithFormat(FormatJSON))

	l.Logger().Debug("debug after reconfigure")
	out := buf.String()
	if !strings.Contains(out, "debug after reconfigure") {
		t.Fatalf("runtime level must survive Reconfigure, got %q", out)
	}
}

func TestShutdownDropsSubscribers(t *testing.T) {
	l := New(WithWriter(&bytes.Buffer{}))

	var deliveries int
	l.OnReconfigure(func(*slog.Logger) { deliveries++ })
	if deliveries != 1 {
		t.Fatalf("expected immediate delivery, got %d", deliveries)
	}

	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	l.Reconfigure(WithFormat(FormatJSON))
	if deliveries != 1 {
		t.Fatalf("Shutdown must drop subscribers, got %d deliveries", deliveries)
	}
}

func TestConcurrentReconfigureAndLog(t *testing.T) {
	l := New(WithWriter(io.Discard))

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Logger().Info("log")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Reconfigure(WithFormat(FormatText), WithReportCaller(true))
				l.SetLevel(slog.LevelInfo)
			}
		}()
	}
	wg.Wait()
}

func TestApplyConfigOmitsPreserveCallerAndStacks(t *testing.T) {
	l := New(WithWriter(io.Discard), WithReportCaller(true), WithStackTraces(true))
	if !l.ReportCaller() || !l.StackTraces() {
		t.Fatal("precondition: caller and stacks should be enabled")
	}

	l.ApplyConfig(LogConfig{Level: "warn"})
	if !l.ReportCaller() {
		t.Fatal("omitted report_caller cleared ReportCaller")
	}
	if !l.StackTraces() {
		t.Fatal("omitted stack_traces cleared StackTraces")
	}
	if l.Level() != slog.LevelWarn {
		t.Fatalf("Level() = %v, want warn", l.Level())
	}

	off := false
	l.ApplyConfig(LogConfig{ReportCaller: &off, StackTraces: &off})
	if l.ReportCaller() || l.StackTraces() {
		t.Fatal("explicit false did not disable caller/stacks")
	}
}

func TestComponentContract(t *testing.T) {
	l := New(WithWriter(&bytes.Buffer{}))
	if l.Name() != ComponentName {
		t.Fatalf("Name() = %q, want %q", l.Name(), ComponentName)
	}
	if l.GetInitOrderStage() != cf.LogsStage {
		t.Fatalf("GetInitOrderStage() = %q, want %q", l.GetInitOrderStage(), cf.LogsStage)
	}
	if err := l.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := l.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestFrameworkIntegration(t *testing.T) {
	fw := cf.New() // LogsStage is part of the built-in bootstrap prefix
	l := New(WithWriter(&bytes.Buffer{}))
	if err := fw.AddComponent(l); err != nil {
		t.Fatalf("AddComponent: %v", err)
	}

	order, err := fw.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(order) != 1 || order[0] != l {
		t.Fatalf("expected the logs component in resolved order, got %v", order)
	}

	if err := fw.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got, ok := cf.Get[*Logs](fw)
	if !ok || got != l {
		t.Fatalf("Get[*Logs] did not return the registered component")
	}
	if err := fw.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestSetLevelForIsolatesComponent(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelInfo))

	var vpqLog, pgLog *slog.Logger
	subV := l.OnReconfigureFor("vpq", func(nl *slog.Logger) { vpqLog = nl })
	defer subV.Unsubscribe()
	subP := l.OnReconfigureFor("postgresql", func(nl *slog.Logger) { pgLog = nl })
	defer subP.Unsubscribe()

	l.SetLevelFor("vpq", slog.LevelDebug)
	vpqLog.Debug("vpq-debug")
	pgLog.Debug("pg-debug")
	l.Logger().Debug("global-debug")

	out := buf.String()
	if !strings.Contains(out, "vpq-debug") {
		t.Fatalf("vpq debug must pass with SetLevelFor, got %q", out)
	}
	if strings.Contains(out, "pg-debug") || strings.Contains(out, "global-debug") {
		t.Fatalf("other loggers must stay at global Info, got %q", out)
	}
}

func TestSetLevelForQuieterThanGlobal(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelDebug))

	var vpqLog *slog.Logger
	sub := l.OnReconfigureFor("vpq", func(nl *slog.Logger) { vpqLog = nl })
	defer sub.Unsubscribe()

	l.SetLevelFor("vpq", slog.LevelWarn)
	vpqLog.Debug("vpq-quiet")
	vpqLog.Warn("vpq-warn")
	l.Logger().Debug("global-debug")

	out := buf.String()
	if strings.Contains(out, "vpq-quiet") {
		t.Fatalf("component override must suppress debug, got %q", out)
	}
	if !strings.Contains(out, "vpq-warn") || !strings.Contains(out, "global-debug") {
		t.Fatalf("warn and global debug must emit, got %q", out)
	}
}

func TestResetLevelFallsBackToGlobal(t *testing.T) {
	var buf bytes.Buffer
	l := New(WithWriter(&buf), WithLevel(slog.LevelInfo))

	var vpqLog *slog.Logger
	sub := l.OnReconfigureFor("vpq", func(nl *slog.Logger) { vpqLog = nl })
	defer sub.Unsubscribe()

	l.SetLevelFor("vpq", slog.LevelDebug)
	if l.LevelFor("vpq") != slog.LevelDebug {
		t.Fatalf("LevelFor(vpq) = %v, want Debug", l.LevelFor("vpq"))
	}
	l.ResetLevel("vpq")
	if l.LevelFor("vpq") != slog.LevelInfo {
		t.Fatalf("LevelFor after Reset = %v, want Info", l.LevelFor("vpq"))
	}

	vpqLog.Debug("after-reset")
	if strings.Contains(buf.String(), "after-reset") {
		t.Fatal("debug must be filtered after ResetLevel")
	}
}

func TestSetLevelForDoesNotRedeliver(t *testing.T) {
	l := New(WithWriter(&bytes.Buffer{}))

	var deliveries int
	sub := l.OnReconfigureFor("vpq", func(*slog.Logger) { deliveries++ })
	defer sub.Unsubscribe()
	if deliveries != 1 {
		t.Fatalf("expected immediate delivery, got %d", deliveries)
	}

	l.SetLevelFor("vpq", slog.LevelDebug)
	if deliveries != 1 {
		t.Fatalf("SetLevelFor must not redeliver, got %d", deliveries)
	}
}

// Ensure the compile-time contract holds.
var _ cf.CaerusComponent = (*Logs)(nil)
