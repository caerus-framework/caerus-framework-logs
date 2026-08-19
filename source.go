package cf_logs

import (
	"fmt"
	"strings"

	cf "github.com/caerus-framework/caerus-framework"
)

// Compile-time assertion: logs is a CoreConfigSource.
var _ cf.CoreConfigSource = (*Logs)(nil)

// CoreConfigSource implements cf.CoreConfigSource. It declares the logs
// component's own configuration source; the logs module cannot import the
// configuration module (the configuration module imports logs), so the
// framework discovers it among registered components during argv absorption
// and registers the declaration on the component's behalf.
//
// The source is owned by the component: default file config/<name>.json, env
// prefix LOGS_, owner cf_logs. An argv redeclaration wins: the --<name>
// file-path flag ParseFlags registers overrides where the file is read from,
// and the loaded value reaches the component through OnConfigReload (see
// WithConfigSource). No source is declared when WithConfigSource was not given.
func (l *Logs) CoreConfigSource() ([]cf.ConfigSourceValue, error) {
	name := l.configSource
	if name == "" {
		return nil, nil
	}
	return []cf.ConfigSourceValue{{
		Name:      name,
		Path:      "config/" + name + ".json",
		Format:    "json",
		EnvPrefix: "LOGS_",
		Owner:     ComponentName,
		Validate:  validateLogConfigValue,
		Sample:    LogConfig{},
	}}, nil
}

func validateLogConfigValue(v any) error {
	cfg, ok := v.(*LogConfig)
	if !ok {
		return fmt.Errorf("cf_logs: validate config: unexpected type %T", v)
	}
	if cfg.Format != "" {
		if _, err := ParseFormat(cfg.Format); err != nil {
			return err
		}
	}
	if cfg.Level != "" {
		if _, err := ParseLevel(cfg.Level); err != nil {
			return err
		}
	}
	if cfg.StackLevel != "" {
		if _, err := ParseLevel(cfg.StackLevel); err != nil {
			return err
		}
	}
	for name, raw := range cfg.ComponentLevels {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := ParseLevel(raw); err != nil {
			return fmt.Errorf("cf_logs: component_levels[%q]: %w", name, err)
		}
	}
	return nil
}
