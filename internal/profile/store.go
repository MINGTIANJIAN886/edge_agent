package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Store persists the robot profile as JSON, written atomically via a
// temp file + rename so a crash never leaves a half-written profile.
type Store struct {
	Path        string
	DeviceID    string
	Version     int
}

func (s *Store) dir() string {
	return filepath.Dir(s.Path)
}

func (s *Store) Load() (*Profile, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &p, nil
}

func (s *Store) Update(name string, res CapabilityResult) error {
	p := &Profile{
		DeviceID:       s.DeviceID,
		ProfileVersion: s.Version,
		UpdatedAt:      time.Now(),
		Capabilities:   map[string]CapabilityResult{},
	}
	if existing, err := s.Load(); err == nil {
		p.Capabilities = existing.Capabilities
		if p.Capabilities == nil {
			p.Capabilities = map[string]CapabilityResult{}
		}
		p.Task = existing.Task
		p.Model = existing.Model
	}
	p.Capabilities[name] = res
	return s.Save(p)
}

func (s *Store) Save(p *Profile) error {
	if err := os.MkdirAll(s.dir(), 0755); err != nil {
		return fmt.Errorf("mkdir profile dir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
