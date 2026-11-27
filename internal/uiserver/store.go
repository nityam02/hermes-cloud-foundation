package uiserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Connection is what the page remembers between runs of `hermes ui`, so a restart does not mean
// starting again. It holds names and identifiers only: no credential, token or key is ever stored.
type Connection struct {
	AWS struct {
		Profile    string `json:"profile"`
		Region     string `json:"region"`
		Identity   string `json:"identity"` // the ARN the last sign-in check returned
		Account    string `json:"account"`
		RoleARN    string `json:"role_arn"`
		ExternalID string `json:"external_id"` // not a secret: it ties the role to this setup (AWS guidance)
		OK         bool   `json:"ok"`
		Checked    string `json:"checked,omitempty"` // when the last passing check ran (RFC 3339)
		Summary    string `json:"summary,omitempty"`
	} `json:"aws"`
	GCP struct {
		Account   string `json:"account"`
		ProjectID string `json:"project_id"`
		Region    string `json:"region"`
		OK        bool   `json:"ok"`
		Checked   string `json:"checked,omitempty"`
		Summary   string `json:"summary,omitempty"`
	} `json:"gcp"`
	Regions string `json:"regions"`
}

// store keeps the Connection in memory and, when a directory is set, in connection.json (0600).
type store struct {
	mu   sync.Mutex
	dir  string
	conn Connection
}

func newStore(dir string) *store {
	s := &store{dir: dir}
	if dir != "" {
		if data, err := os.ReadFile(filepath.Join(dir, "connection.json")); err == nil {
			_ = json.Unmarshal(data, &s.conn)
		}
	}
	return s
}

func (s *store) get() Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *store) set(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = c
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("save connection: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "connection.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save connection: %w", err)
	}
	return os.Rename(tmp, filepath.Join(s.dir, "connection.json"))
}
