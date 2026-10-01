// Package config stores the employee-controlled cleaner settings and the
// agent's local state (enrollment, last scan, last shared snapshot).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
)

const (
	DeleteTrash     = "trash"
	DeletePermanent = "permanent"
)

// Config holds the cleaner settings. Only the employee can change these; an
// organization can never choose which folders are scanned or deleted.
type Config struct {
	ScanRoots       []string `json:"scan_roots"`
	Exclude         []string `json:"exclude"`
	DisabledRules   []string `json:"disabled_rules"`
	StaleDays       int      `json:"stale_days"`
	DeleteMode      string   `json:"delete_mode"`
	AlwaysPreview   bool     `json:"always_preview"`
	CleanDirtyRepos bool     `json:"clean_dirty_repos"`
	MaxDepth        int      `json:"max_depth"`
	Schedule        string   `json:"schedule"`
	AIMode          string   `json:"ai_mode"`
}

var defaultRootCandidates = []string{
	"~/Projects", "~/projects", "~/Developer", "~/code", "~/Code", "~/src", "~/dev",
	"~/workspace", "~/Workspace", "~/repos", "~/git", "~/go/src", "~/IdeaProjects",
	"~/StudioProjects", "~/AndroidStudioProjects",
}

func Default() Config {
	var roots []string
	seen := map[string]bool{}
	for _, c := range defaultRootCandidates {
		p := rules.ExpandPath(c)
		real, err := filepath.EvalSymlinks(p)
		if err != nil || seen[strings.ToLower(real)] {
			continue // missing, or same folder on a case-insensitive disk
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			seen[strings.ToLower(real)] = true
			roots = append(roots, p)
		}
	}
	return Config{
		ScanRoots:     roots,
		Exclude:       []string{},
		DisabledRules: []string{},
		StaleDays:     60,
		DeleteMode:    DeleteTrash,
		AlwaysPreview: true,
		MaxDepth:      8,
		Schedule:      "manual",
		AIMode:        "off",
	}
}

// Dir is the agent's data directory, e.g. ~/Library/Application Support/Tidyfleet.
func Dir() (string, error) {
	if d := os.Getenv("TIDYFLEET_HOME"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "Tidyfleet")
	return d, os.MkdirAll(d, 0o700)
}

// Path returns a file path inside the data directory.
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

func Load() (Config, error) {
	p, err := Path("config.json")
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := ReadJSON(p, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("reading %s: %w", p, err)
	}
	return cfg, nil
}

func Save(cfg Config) error {
	p, err := Path("config.json")
	if err != nil {
		return err
	}
	return WriteJSON(p, cfg)
}

// Set updates one setting from its string form, as typed on the command line.
func (c *Config) Set(key, value string) error {
	list := func() []string {
		var out []string
		for _, v := range strings.Split(value, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		if out == nil {
			out = []string{}
		}
		return out
	}
	switch key {
	case "scan_roots":
		c.ScanRoots = nil
		for _, p := range list() {
			c.ScanRoots = append(c.ScanRoots, rules.ExpandPath(p))
		}
	case "exclude":
		c.Exclude = nil
		for _, p := range list() {
			c.Exclude = append(c.Exclude, rules.ExpandPath(p))
		}
	case "disabled_rules":
		c.DisabledRules = list()
	case "stale_days", "max_depth":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("%s must be a non-negative number", key)
		}
		if key == "stale_days" {
			c.StaleDays = n
		} else {
			c.MaxDepth = n
		}
	case "delete_mode":
		if value != DeleteTrash && value != DeletePermanent {
			return fmt.Errorf("delete_mode must be trash or permanent")
		}
		c.DeleteMode = value
	case "always_preview", "clean_dirty_repos":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		if key == "always_preview" {
			c.AlwaysPreview = b
		} else {
			c.CleanDirtyRepos = b
		}
	case "schedule":
		if value != "manual" && value != "weekly" {
			return fmt.Errorf("schedule must be manual or weekly")
		}
		c.Schedule = value
	case "ai_mode":
		if value != "off" && value != "local" && value != "cloud" {
			return fmt.Errorf("ai_mode must be off, local or cloud")
		}
		c.AIMode = value
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

// RuleEnabled reports whether the user has left a rule switched on.
func (c Config) RuleEnabled(id string) bool {
	for _, d := range c.DisabledRules {
		if d == id {
			return false
		}
	}
	return true
}

func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteJSON writes atomically so a crash never leaves a half-written file.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
