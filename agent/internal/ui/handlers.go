package ui

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/cleaner"
	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
	"github.com/tidyfleet/tidyfleet/agent/internal/tree"
)

type itemView struct {
	ID          string    `json:"id"`
	RuleID      string    `json:"rule_id"`
	RuleName    string    `json:"rule_name"`
	Ecosystem   string    `json:"ecosystem"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	Size        int64     `json:"size"`
	Eligible    bool      `json:"eligible"`
	SkipReason  string    `json:"skip_reason,omitempty"`
	StaleDays   int       `json:"stale_days"`
	LastActive  time.Time `json:"last_active,omitempty"`
	GitDirty    bool      `json:"git_dirty,omitempty"`
	CleanMethod string    `json:"clean_method"`
	Action      string    `json:"action"`
}

func view(it scanner.Item, all []rules.Rule, permanent bool) itemView {
	v := itemView{
		ID: it.ID, RuleID: it.RuleID, RuleName: it.RuleName, Ecosystem: it.Ecosystem, Kind: it.Kind,
		Path: it.Path, Detail: it.Detail, Size: it.SizeBytes, Eligible: it.Eligible, SkipReason: it.SkipReason,
		StaleDays: it.StaleDays, LastActive: it.LastActive, GitDirty: it.GitDirty, CleanMethod: it.CleanMethod,
	}
	switch {
	case it.CleanMethod == rules.MethodCommand:
		v.Action = "Runs the tool's own clean command"
		for _, r := range all {
			if r.ID == it.RuleID && len(r.Clean.Commands) > 0 {
				cmds := make([]string, len(r.Clean.Commands))
				for i, c := range r.Clean.Commands {
					cmds[i] = strings.Join(c, " ")
				}
				v.Action = "Runs " + strings.Join(cmds, "; ")
			}
		}
	case permanent:
		v.Action = "Deletes permanently"
	default:
		v.Action = "Moves to Trash"
	}
	return v
}

func totals(rep *scanner.Report) (reclaim, skipped int64, eligible int) {
	for _, it := range rep.Items {
		if it.Eligible {
			reclaim += it.SizeBytes
			eligible++
		} else {
			skipped += it.SizeBytes
		}
	}
	return
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()
	all, _ := loadRules()
	t, rep := s.snapshot()
	s.mu.RLock()
	out := map[string]any{
		"version":     s.Version,
		"scanning":    s.scanning,
		"visited":     s.visited.Load(),
		"scan_error":  s.scanErr,
		"scan_roots":  cfg.ScanRoots,
		"delete_mode": cfg.DeleteMode,
		"platform":    runtime.GOOS,
		"home":        homeDir(),
	}
	s.mu.RUnlock()
	if e, _ := reporter.LoadEnrollment(); e != nil {
		out["org_name"] = e.OrgName
	}
	if rep != nil {
		reclaim, skipped, n := totals(rep)
		out["scanned_at"] = rep.ScannedAt
		out["reclaimable"] = reclaim
		out["skipped"] = skipped
		out["eligible_count"] = n
		out["warnings"] = rep.Warnings
		// Caches and tools live outside the scan folders, so the tree does not show them.
		extras := []itemView{}
		for _, it := range rep.Items {
			if it.Kind != rules.KindProject && (t == nil || !t.Contains(it.Path)) {
				extras = append(extras, view(it, all, cfg.DeleteMode == config.DeletePermanent))
			}
		}
		out["extras"] = extras
		type pick struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		eligible := []pick{}
		for _, it := range rep.Items {
			if it.Eligible {
				name := it.Path
				if name == "" {
					name = it.RuleName
				}
				eligible = append(eligible, pick{it.ID, name, it.SizeBytes})
			}
		}
		out["eligible_items"] = eligible
	}
	if t != nil {
		out["roots"] = t.Roots()
		out["tree_scanned_at"] = t.ScannedAt()
	}
	writeJSON(w, http.StatusOK, out)
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// items lists every rule match from the last scan, safe ones first, so the
// UI can show them all with checkboxes and explain the ones that were kept.
func (s *Server) items(w http.ResponseWriter, r *http.Request) {
	_, rep := s.snapshot()
	out := []itemView{}
	if rep != nil {
		cfg, _ := config.Load()
		all, _ := loadRules()
		for _, it := range rep.Items { // already sorted largest first
			if it.Eligible {
				out = append(out, view(it, all, cfg.DeleteMode == config.DeletePermanent))
			}
		}
		for _, it := range rep.Items {
			if !it.Eligible {
				out = append(out, view(it, all, cfg.DeleteMode == config.DeletePermanent))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if r.ContentLength > 0 && !decode(w, r, &req) {
		return
	}
	if req.Path != "" {
		if t, _ := s.snapshot(); t == nil || !t.Contains(req.Path) {
			writeErr(w, http.StatusNotFound, "folder is not in the scanned tree")
			return
		}
	}
	if !s.startScan(s.ctx, req.Path) {
		writeErr(w, http.StatusConflict, "a scan is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

type entryView struct {
	tree.Entry
	Item *itemView `json:"item,omitempty"`
}

func (s *Server) treeLevel(w http.ResponseWriter, r *http.Request) {
	t, rep := s.snapshot()
	if t == nil {
		writeErr(w, http.StatusConflict, "no scan yet")
		return
	}
	cfg, _ := config.Load()
	all, _ := loadRules()
	byPath := map[string]scanner.Item{}
	if rep != nil {
		for _, it := range rep.Items {
			if it.Path != "" {
				byPath[it.Path] = it
			}
		}
	}
	decorate := func(es []tree.Entry) []entryView {
		out := make([]entryView, len(es))
		for i, e := range es {
			out[i].Entry = e
			if it, ok := byPath[e.Path]; ok && e.Path != "" {
				v := view(it, all, cfg.DeleteMode == config.DeletePermanent)
				out[i].Item = &v
			}
		}
		return out
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSON(w, http.StatusOK, map[string]any{"entries": decorate(t.Roots())})
		return
	}
	self, kids, ok := t.Children(path)
	if !ok {
		writeErr(w, http.StatusNotFound, "folder is not in the scanned tree")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"self": self, "entries": decorate(kids)})
}

type idsRequest struct {
	IDs     []string `json:"ids"`
	Confirm string   `json:"confirm"`
}

// preview is the dry run: what would happen to the selected items. Nothing changes.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req idsRequest
	if !decode(w, r, &req) {
		return
	}
	_, rep := s.snapshot()
	if rep == nil {
		writeErr(w, http.StatusConflict, "no scan yet")
		return
	}
	cfg, _ := config.Load()
	all, _ := loadRules()
	want := map[string]bool{}
	for _, id := range req.IDs {
		want[id] = true
	}
	items := []itemView{}
	var total int64
	for _, it := range rep.Items {
		if want[it.ID] && it.Eligible {
			items = append(items, view(it, all, cfg.DeleteMode == config.DeletePermanent))
			total += it.SizeBytes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "delete_mode": cfg.DeleteMode})
}

// clean re-scans first so every safety check runs on current data, then
// cleans only selected items that are still eligible.
func (s *Server) clean(w http.ResponseWriter, r *http.Request) {
	var req idsRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing selected")
		return
	}
	if !s.scanMu.TryLock() {
		writeErr(w, http.StatusConflict, "a scan is running; try again when it finishes")
		return
	}
	defer s.scanMu.Unlock()

	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.DeleteMode == config.DeletePermanent && req.Confirm != "delete" {
		writeErr(w, http.StatusBadRequest, `delete mode is permanent: confirm by typing "delete"`)
		return
	}
	all, err := loadRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	fresh := scanner.Scan(r.Context(), cfg, all)
	want := map[string]bool{}
	for _, id := range req.IDs {
		want[id] = true
	}
	var selected []scanner.Item
	skipped := []string{}
	for _, it := range fresh.Items {
		if !want[it.ID] {
			continue
		}
		delete(want, it.ID)
		if it.Eligible {
			selected = append(selected, it)
		} else {
			skipped = append(skipped, it.RuleName+" "+it.Path+": "+it.SkipReason)
		}
	}
	for id := range want {
		skipped = append(skipped, "item "+id+" no longer exists")
	}
	res, err := cleaner.Clean(r.Context(), cfg, all, selected)

	// Bring the cached tree and report up to date.
	cleaned := map[string]bool{}
	t, _ := s.snapshot()
	for _, e := range res.Entries {
		if e.OK {
			cleaned[e.RuleID+"\x00"+e.Path] = true
			if t != nil && e.Path != "" {
				t.Remove(e.Path)
			}
		}
	}
	kept := fresh.Items[:0]
	for _, it := range fresh.Items {
		if !cleaned[it.RuleID+"\x00"+it.Path] {
			kept = append(kept, it)
		}
	}
	fresh.Items = kept
	fresh.ReclaimableBytes, fresh.SkippedBytes, _ = totals(&fresh)
	_ = reporter.SaveLastScan(fresh)
	s.mu.Lock()
	s.report = &fresh
	s.mu.Unlock()

	out := map[string]any{"entries": res.Entries, "freed": res.FreedBytes, "failed": res.Failed, "skipped": skipped, "delete_mode": cfg.DeleteMode}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

type pathRequest struct {
	Path string `json:"path"`
}

// known reports whether path is something the UI is allowed to act on.
func (s *Server) known(p string) bool {
	t, rep := s.snapshot()
	if t != nil && t.Contains(p) {
		return true
	}
	if rep != nil {
		for _, it := range rep.Items {
			if it.Path != "" && it.Path == p {
				return true
			}
		}
	}
	return false
}

func (s *Server) reveal(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if !decode(w, r, &req) {
		return
	}
	p := filepath.Clean(req.Path)
	if !filepath.IsAbs(p) || !s.known(p) {
		writeErr(w, http.StatusNotFound, "unknown folder")
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", p)
	case "windows":
		cmd = exec.Command("explorer", "/select,", p)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(p))
	}
	if err := cmd.Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	go cmd.Wait()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) exclude(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if !decode(w, r, &req) {
		return
	}
	p := filepath.Clean(req.Path)
	t, rep := s.snapshot()
	if !filepath.IsAbs(p) || !s.known(p) {
		writeErr(w, http.StatusNotFound, "unknown folder")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, root := range cfg.ScanRoots {
		if filepath.Clean(root) == p {
			writeErr(w, http.StatusBadRequest, "that is a scan location; remove it in Settings instead")
			return
		}
	}
	if !scanner.IsUnder(p, cfg.Exclude) {
		cfg.Exclude = append(cfg.Exclude, p)
		if err := config.Save(cfg); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if t != nil {
		t.Lock(p)
	}
	if rep != nil {
		s.mu.Lock()
		r2 := *rep
		r2.Items = nil
		for _, it := range rep.Items {
			if it.Path == "" || !scanner.IsUnder(it.Path, []string{p}) {
				r2.Items = append(r2.Items, it)
			}
		}
		r2.ReclaimableBytes, r2.SkippedBytes, _ = totals(&r2)
		s.report = &r2
		s.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{"config": cfg}
	if e, _ := reporter.LoadEnrollment(); e != nil {
		out["managed"] = map[string]any{"org_name": e.OrgName, "allow_ai": e.Policy.AllowAI, "report_interval_minutes": e.Policy.ReportIntervalMinutes}
	}
	dir, _ := config.Dir()
	out["config_dir"] = dir
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Key == "ai_mode" && req.Value != "off" {
		if e, _ := reporter.LoadEnrollment(); e != nil && !e.Policy.AllowAI {
			writeErr(w, http.StatusForbidden, "Managed by "+e.OrgName+": the AI assistant is turned off")
			return
		}
	}
	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := cfg.Set(req.Key, req.Value); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.Save(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg})
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()
	all, err := loadRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type ruleView struct {
		rules.Rule
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	}
	out := []ruleView{}
	for _, rl := range all {
		v := ruleView{Rule: rl, Enabled: cfg.RuleEnabled(rl.ID), Status: "active"}
		switch {
		case !rl.AppliesToOS():
			v.Status = "not used on this OS"
		case !rl.Available():
			v.Status = rl.Match.RequiresCommand + " not installed"
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	entries, err := cleaner.ReadLog()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time.After(entries[j].Time) })
	if len(entries) > 500 {
		entries = entries[:500]
	}
	if entries == nil {
		entries = []cleaner.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, reporter.Collect(r.Context(), s.Version, false))
}

func (s *Server) sharing(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enrolled": false, "queued": reporter.QueueLen()}
	if e, _ := reporter.LoadEnrollment(); e != nil {
		out["enrolled"] = true
		// Never send the device token to the page.
		out["org_name"], out["server_url"], out["enrolled_at"], out["policy"] = e.OrgName, e.ServerURL, e.EnrolledAt, e.Policy
	}
	if rec, _ := reporter.LastShared(); rec != nil {
		out["last_shared"] = rec
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerURL string `json:"server_url"`
		Code      string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	snap := reporter.Collect(r.Context(), s.Version, false)
	e, err := reporter.Enroll(r.Context(), req.ServerURL, req.Code, snap.Metrics.OSVersion, s.Version)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = reporter.Report(r.Context(), e, snap)
	s.sharing(w, r)
}

func (s *Server) leave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if r.ContentLength > 0 && !decode(w, r, &req) {
		return
	}
	if err := reporter.Leave(r.Context(), req.Force); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.sharing(w, r)
}

func (s *Server) sendReport(w http.ResponseWriter, r *http.Request) {
	e, err := reporter.LoadEnrollment()
	if err != nil || e == nil {
		writeErr(w, http.StatusBadRequest, "this Mac is not enrolled")
		return
	}
	if _, err := reporter.Report(r.Context(), e, reporter.Collect(r.Context(), s.Version, false)); err != nil {
		if errors.Is(err, reporter.ErrDeviceRemoved) {
			writeErr(w, http.StatusGone, err.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, "queued; sending failed: "+err.Error())
		return
	}
	s.sharing(w, r)
}
