// Package cleaner removes items chosen from a scan report and records every
// action in a local log. It re-checks safety guards right before acting.
package cleaner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/fsutil"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

type LogEntry struct {
	Time      time.Time `json:"time"`
	RuleID    string    `json:"rule_id"`
	Path      string    `json:"path,omitempty"`
	Bytes     int64     `json:"bytes"`
	Method    string    `json:"method"`
	TrashedTo string    `json:"trashed_to,omitempty"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
}

type Result struct {
	Entries    []LogEntry `json:"entries"`
	FreedBytes int64      `json:"freed_bytes"`
	Failed     int        `json:"failed"`
}

// Clean removes the given eligible items. Nothing outside the items is touched.
func Clean(ctx context.Context, cfg config.Config, all []rules.Rule, items []scanner.Item) (Result, error) {
	byID := map[string]rules.Rule{}
	for _, r := range all {
		byID[r.ID] = r
	}
	var res Result
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		e := LogEntry{Time: time.Now(), RuleID: it.RuleID, Path: it.Path, Bytes: it.SizeBytes, Method: it.CleanMethod}
		r, ok := byID[it.RuleID]
		var err error
		switch {
		case !ok:
			err = fmt.Errorf("unknown rule %s", it.RuleID)
		case !it.Eligible:
			err = fmt.Errorf("not eligible: %s", it.SkipReason)
		case it.CleanMethod == rules.MethodCommand:
			err = runCommands(ctx, r, it)
		default:
			if err = checkSafe(cfg, r, it); err == nil {
				if cfg.DeleteMode == config.DeletePermanent {
					e.Method = "permanent"
					err = os.RemoveAll(it.Path)
				} else {
					e.TrashedTo, err = fsutil.MoveToTrash(it.Path)
				}
			}
		}
		if err != nil {
			e.Error = err.Error()
			res.Failed++
		} else {
			e.OK = true
			res.FreedBytes += it.SizeBytes
		}
		res.Entries = append(res.Entries, e)
		if lerr := appendLog(e); lerr != nil {
			return res, fmt.Errorf("writing cleanup log: %w", lerr)
		}
	}
	return res, nil
}

// checkSafe is the last line of defense before a folder is removed.
func checkSafe(cfg config.Config, r rules.Rule, it scanner.Item) error {
	p := it.Path
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return errors.New("refusing: path is not absolute and clean")
	}
	home, _ := os.UserHomeDir()
	if p == "/" || p == home || filepath.Dir(p) == p {
		return errors.New("refusing: protected location")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return errors.New("refusing: not a real folder")
	}
	if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
		return errors.New("refusing: folder is a git repository")
	}
	if scanner.IsUnder(p, cfg.Exclude) {
		return errors.New("refusing: folder is excluded in settings")
	}
	for _, root := range cfg.ScanRoots {
		if p == filepath.Clean(root) {
			return errors.New("refusing: folder is a scan root")
		}
	}
	switch r.Kind {
	case rules.KindProject:
		if !scanner.IsUnder(p, cfg.ScanRoots) {
			return errors.New("refusing: folder is outside the scan locations")
		}
		matched := false
		for _, n := range r.Match.DirNames {
			matched = matched || filepath.Base(p) == n
		}
		if !matched {
			return errors.New("refusing: folder name does not match its rule")
		}
	case rules.KindPath:
		known := false
		for _, rp := range r.ResolvePaths() {
			known = known || rp == p
		}
		if !known {
			return errors.New("refusing: path is not one of the rule's known locations")
		}
	default:
		return errors.New("refusing: rule kind cannot be moved to trash")
	}
	return nil
}

func runCommands(ctx context.Context, r rules.Rule, it scanner.Item) error {
	for _, argv := range r.Clean.Commands {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		out, err := exec.CommandContext(cctx, argv[0], argv[1:]...).CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(argv, " "), err, lastLine(out))
		}
	}
	return nil
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}

func appendLog(e LogEntry) error {
	p, err := config.Path("cleanup-log.jsonl")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadLog returns logged cleanups, newest last.
func ReadLog() ([]LogEntry, error) {
	p, err := config.Path("cleanup-log.jsonl")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LogEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Record appends an entry for an action taken outside Clean, such as a
// folder the user trashed or moved by hand in the UI.
func Record(e LogEntry) error { return appendLog(e) }
