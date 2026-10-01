// Package rules loads the YAML cleanup rules that describe what the cleaner
// may match, how it verifies a match, and how it cleans it.
package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

const (
	KindProject = "project" // a folder found while walking scan roots, next to a project marker
	KindPath    = "path"    // a fixed, well-known cache location
	KindProbe   = "probe"   // sized and cleaned through a tool (docker, simctl)

	MethodTrash   = "trash"
	MethodCommand = "command"
)

type Rule struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Ecosystem   string   `yaml:"ecosystem" json:"ecosystem"`
	Description string   `yaml:"description" json:"description"`
	Kind        string   `yaml:"kind" json:"kind"`
	OS          []string `yaml:"os,omitempty" json:"os,omitempty"`
	// StaleCheck makes the rule respect the staleness threshold, based on
	// the last activity in the surrounding project.
	StaleCheck bool `yaml:"stale_check" json:"stale_check"`
	// RequireGitignored makes a folder inside a git repo eligible only if
	// git reports it as ignored. Folders tracked by git are never eligible.
	RequireGitignored bool   `yaml:"require_gitignored" json:"require_gitignored"`
	Match             Match  `yaml:"match" json:"match"`
	Clean             Clean  `yaml:"clean" json:"clean"`
	Source            string `yaml:"-" json:"source"`
}

type Match struct {
	DirNames        []string            `yaml:"dir_names,omitempty" json:"dir_names,omitempty"`
	SiblingAny      []string            `yaml:"sibling_any,omitempty" json:"sibling_any,omitempty"`
	Paths           map[string][]string `yaml:"paths,omitempty" json:"paths,omitempty"`
	PathCommand     []string            `yaml:"path_command,omitempty" json:"path_command,omitempty"`
	RequiresCommand string              `yaml:"requires_command,omitempty" json:"requires_command,omitempty"`
	Probe           string              `yaml:"probe,omitempty" json:"probe,omitempty"`
}

type Clean struct {
	Method   string     `yaml:"method" json:"method"`
	Commands [][]string `yaml:"commands,omitempty" json:"commands,omitempty"`
	// Fallback is used when the clean command's binary is not installed.
	Fallback string `yaml:"fallback,omitempty" json:"fallback,omitempty"`
}

// AppliesToOS reports whether the rule runs on the current platform.
func (r Rule) AppliesToOS() bool {
	if len(r.OS) == 0 {
		return true
	}
	for _, o := range r.OS {
		if o == runtime.GOOS {
			return true
		}
	}
	return false
}

// Available reports whether the rule's required tool is installed.
func (r Rule) Available() bool {
	if r.Match.RequiresCommand == "" {
		return true
	}
	_, err := exec.LookPath(r.Match.RequiresCommand)
	return err == nil
}

// ResolvePaths expands the rule's fixed paths for the current OS.
func (r Rule) ResolvePaths() []string {
	var out []string
	for _, key := range []string{"all", runtime.GOOS} {
		for _, p := range r.Match.Paths[key] {
			if p = ExpandPath(p); p != "" {
				out = append(out, p)
			}
		}
	}
	if len(r.Match.PathCommand) > 0 {
		if b, err := exec.Command(r.Match.PathCommand[0], r.Match.PathCommand[1:]...).Output(); err == nil {
			if p := strings.TrimSpace(string(b)); p != "" {
				out = append(out, filepath.Clean(p))
			}
		}
	}
	return out
}

func (r Rule) validate() error {
	if r.ID == "" || r.Name == "" {
		return fmt.Errorf("rule needs id and name")
	}
	switch r.Kind {
	case KindProject:
		if len(r.Match.DirNames) == 0 || len(r.Match.SiblingAny) == 0 {
			return fmt.Errorf("rule %s: project rules need dir_names and sibling_any", r.ID)
		}
	case KindPath:
		if len(r.Match.Paths) == 0 && len(r.Match.PathCommand) == 0 {
			return fmt.Errorf("rule %s: path rules need paths or path_command", r.ID)
		}
	case KindProbe:
		if r.Match.Probe == "" {
			return fmt.Errorf("rule %s: probe rules need match.probe", r.ID)
		}
	default:
		return fmt.Errorf("rule %s: unknown kind %q", r.ID, r.Kind)
	}
	switch r.Clean.Method {
	case MethodTrash:
	case MethodCommand:
		if len(r.Clean.Commands) == 0 {
			return fmt.Errorf("rule %s: command method needs commands", r.ID)
		}
	default:
		return fmt.Errorf("rule %s: unknown clean method %q", r.ID, r.Clean.Method)
	}
	if r.Clean.Fallback != "" && r.Clean.Fallback != MethodTrash {
		return fmt.Errorf("rule %s: fallback may only be trash", r.ID)
	}
	return nil
}

// Load returns the built-in rules plus any *.yaml rules in extraDir.
// A user rule with the same id replaces the built-in one.
func Load(extraDir string) ([]Rule, error) {
	byID := map[string]Rule{}
	if err := loadFS(builtinFS, "builtin", "builtin", byID); err != nil {
		return nil, err
	}
	if extraDir != "" {
		if _, err := os.Stat(extraDir); err == nil {
			if err := loadFS(os.DirFS(extraDir), ".", "user", byID); err != nil {
				return nil, err
			}
		}
	}
	out := make([]Rule, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadFS(fsys fs.FS, dir, source string, byID map[string]Rule) error {
	files, err := fs.Glob(fsys, filepath.ToSlash(filepath.Join(dir, "*.yaml")))
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		var r Rule
		if err := yaml.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if err := r.validate(); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		r.Source = source
		byID[r.ID] = r
	}
	return nil
}

// ExpandPath expands a leading ~ and %VAR% / $VAR environment references.
func ExpandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	for {
		i := strings.Index(p, "%")
		if i < 0 {
			break
		}
		j := strings.Index(p[i+1:], "%")
		if j < 0 {
			break
		}
		val := os.Getenv(p[i+1 : i+1+j])
		if val == "" {
			return "" // unresolvable; callers skip empty paths
		}
		p = p[:i] + val + p[i+2+j:]
	}
	return filepath.Clean(os.ExpandEnv(p))
}
