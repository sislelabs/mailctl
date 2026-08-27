// Package store is the seam between mailctl's logic and where configuration
// actually lives. Today there is one implementation, backed by the YAML file
// in the user's home directory. It exists so that a server-side frontend can
// swap in a database without every command reaching for the same file.
package store

import (
	"github.com/sislelabs/mailctl/internal"
)

// Store reads and writes mailctl's configuration.
//
// The interface is deliberately whole-config rather than per-domain: the CLI
// and TUI both mutate a config in memory and persist it once, and narrowing
// this before a second backend exists would be guesswork.
type Store interface {
	Load() (*internal.Config, error)
	Save(cfg *internal.Config) error
}

// YAML is a Store backed by a YAML file on disk.
type YAML struct {
	// Path is the file to read and write. Empty means the default location,
	// resolved at call time so tests can set HOME.
	Path string
}

// NewYAML returns a Store backed by the default config path.
func NewYAML() *YAML { return &YAML{} }

// NewYAMLAt returns a Store backed by an explicit path.
func NewYAMLAt(path string) *YAML { return &YAML{Path: path} }

func (y *YAML) path() string {
	if y.Path != "" {
		return y.Path
	}
	return internal.ConfigPath()
}

func (y *YAML) Load() (*internal.Config, error) {
	return internal.LoadConfigFrom(y.path())
}

func (y *YAML) Save(cfg *internal.Config) error {
	return internal.SaveConfigTo(y.path(), cfg)
}

// Memory is an in-memory Store for tests and dry runs. The zero value holds no
// config and reports the same error shape as a missing file.
type Memory struct {
	Config *internal.Config
	// SaveErr, when set, is returned by Save. Lets tests exercise persistence
	// failures without touching a filesystem.
	SaveErr error
}

func (m *Memory) Load() (*internal.Config, error) {
	if m.Config == nil {
		return nil, internal.ErrNoConfig
	}
	return m.Config, nil
}

func (m *Memory) Save(cfg *internal.Config) error {
	if m.SaveErr != nil {
		return m.SaveErr
	}
	m.Config = cfg
	return nil
}
