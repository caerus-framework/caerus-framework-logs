package cf_logs

import (
	cf "github.com/caerus-framework/caerus-framework"
)

// LogConfig is the file/env/flag-drivable logging configuration loaded through
// the configuration component as the "logs" source. The logs component cannot
// read the configuration component directly (import cycle), so the framework
// delivers the freshly loaded value through OnConfigReload. Empty fields keep
// the current value.
type LogConfig struct {
	// Format is "text" or "json". Empty keeps the current format.
	Format string `json:"format,omitempty" yaml:"format,omitempty" env:"FORMAT" flag:"log-format"`
	// Level is "debug", "info", "warn" or "error". Empty keeps the current
	// process-global level.
	Level string `json:"level,omitempty" yaml:"level,omitempty" env:"LEVEL" flag:"log-level"`
	// ReportCaller records the source file:line of every log call.
	ReportCaller bool `json:"report_caller,omitempty" yaml:"report_caller,omitempty" env:"REPORT_CALLER" flag:"report-caller"`
	// StackTraces attaches a stack traceback to records at or above the stack
	// level (default error).
	StackTraces bool `json:"stack_traces,omitempty" yaml:"stack_traces,omitempty" env:"STACK_TRACES" flag:"stack-traces"`
}

// Register the logs core factory so cf.New(FrameworkOptions) can build the
// always-on logs component. init() runs when the module is imported, which is
// guaranteed by any package that uses cf_logs symbols.
func init() {
	cf.RegisterLogsFactory(func(settings *cf.LogsSettings) (cf.CaerusComponent, error) {
		var opts []Option
		if settings != nil {
			if settings.Format != "" {
				f, err := ParseFormat(settings.Format)
				if err != nil {
					return nil, err
				}
				opts = append(opts, WithFormat(f))
			}
			if settings.Level != "" {
				lv, err := ParseLevel(settings.Level)
				if err != nil {
					return nil, err
				}
				opts = append(opts, WithLevel(lv))
			}
			if settings.ConfigSource != "" {
				opts = append(opts, WithConfigSource(settings.ConfigSource))
			}
		}
		return New(opts...), nil
	})
}
