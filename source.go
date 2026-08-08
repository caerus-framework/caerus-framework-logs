package cf_logs

import (
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
		Sample:    LogConfig{},
	}}, nil
}
