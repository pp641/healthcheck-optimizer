package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/cleaner"
	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

// runScan scans with the current settings and records the reclaimable total.
func runScan(ctx context.Context, quiet bool) (config.Config, []rules.Rule, scanner.Report, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, scanner.Report{}, err
	}
	all, err := loadRules()
	if err != nil {
		return cfg, nil, scanner.Report{}, err
	}
	if len(cfg.ScanRoots) == 0 && !quiet {
		fmt.Fprintln(os.Stderr, "No project folders configured; only well-known caches will be checked.")
		fmt.Fprintln(os.Stderr, "Add some with: tidyfleet config set scan_roots ~/Projects,~/code")
	}
	if !quiet {
		fmt.Fprintln(os.Stderr, "Scanning…")
	}
	rep := scanner.Scan(ctx, cfg, all)
	if ctx.Err() != nil {
		return cfg, all, rep, ctx.Err()
	}
	if err := reporter.SaveLastScan(rep); err != nil && !quiet {
		fmt.Fprintln(os.Stderr, "warning: could not save scan total:", err)
	}
	return cfg, all, rep, nil
}

func cmdScan(ctx context.Context, args []string) error {
	fs := newFlags("scan", "[flags]")
	asJSON := fs.Bool("json", false, "print the full report as JSON")
	showAll := fs.Bool("all", false, "list skipped items too, with the reason")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	_, _, rep, err := runScan(ctx, *asJSON)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(rep)
	}
	var eligible, skipped []scanner.Item
	for _, it := range rep.Items {
		if it.Eligible {
			eligible = append(eligible, it)
		} else {
			skipped = append(skipped, it)
		}
	}
	fmt.Printf("Scanned %d location(s), %d folders in %s\n\n",
		len(rep.Roots), rep.DirsVisited, time.Duration(rep.DurationMS)*time.Millisecond)
	if len(eligible) == 0 {
		fmt.Println("Nothing to clean right now.")
	} else {
		fmt.Printf("Reclaimable: %s in %d item(s)\n", humanBytes(rep.ReclaimableBytes), len(eligible))
		printItems(eligible, false)
	}
	if len(skipped) > 0 {
		fmt.Printf("\nSkipped for safety: %s in %d item(s)", humanBytes(rep.SkippedBytes), len(skipped))
		if *showAll {
			fmt.Println()
			printItems(skipped, true)
		} else {
			fmt.Println(" (use --all to see why)")
		}
	}
	for _, w := range rep.Warnings {
		fmt.Fprintln(os.Stderr, "note:", w)
	}
	if len(eligible) > 0 {
		fmt.Println("\nRun `tidyfleet clean` to review and clean these.")
	}
	return nil
}

func printItems(items []scanner.Item, withReason bool) {
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	header := "  ID\tSIZE\tRULE\tIDLE\tLOCATION"
	if withReason {
		header += "\tREASON"
	}
	fmt.Fprintln(tw, header)
	for _, it := range items {
		idle := "-"
		if it.Kind == rules.KindProject && !it.LastActive.IsZero() {
			idle = fmt.Sprintf("%dd", it.StaleDays)
		}
		loc := tildePath(it.Path)
		if loc == "" {
			loc = it.Detail
		}
		line := fmt.Sprintf("  %s\t%s\t%s\t%s\t%s", it.ID, humanBytes(it.SizeBytes), it.RuleName, idle, loc)
		if withReason {
			line += "\t" + it.SkipReason
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
}

func cmdClean(ctx context.Context, args []string) error {
	fs := newFlags("clean", "[flags]")
	yes := fs.Bool("yes", false, "clean without asking for confirmation")
	dryRun := fs.Bool("dry-run", false, "only show what would be cleaned")
	ruleList := fs.String("rule", "", "only clean items from these rule ids (comma-separated)")
	idList := fs.String("only", "", "only clean these item ids from the scan (comma-separated)")
	minSize := fs.Int64("min-mb", 0, "skip items smaller than this many megabytes")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, all, rep, err := runScan(ctx, false)
	if err != nil {
		return err
	}
	ruleSet, idSet := csvSet(*ruleList), csvSet(*idList)
	var selected []scanner.Item
	var total int64
	for _, it := range rep.Items {
		if !it.Eligible || it.SizeBytes < *minSize*1_000_000 {
			continue
		}
		if (len(ruleSet) > 0 && !ruleSet[it.RuleID]) || (len(idSet) > 0 && !idSet[it.ID]) {
			continue
		}
		selected = append(selected, it)
		total += it.SizeBytes
	}
	if len(selected) == 0 {
		fmt.Println("Nothing to clean.")
		return nil
	}

	permanent := cfg.DeleteMode == config.DeletePermanent
	fmt.Printf("\nPreview: %d item(s), %s\n", len(selected), humanBytes(total))
	// With always_preview off, --yes runs skip the item list (the log still has it).
	if cfg.AlwaysPreview || !*yes || *dryRun {
		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tSIZE\tRULE\tACTION\tLOCATION")
		for _, it := range selected {
			loc := tildePath(it.Path)
			if loc == "" {
				loc = it.Detail
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", it.ID, humanBytes(it.SizeBytes), it.RuleName, actionFor(it, all, permanent), loc)
		}
		tw.Flush()
	}
	if *dryRun {
		fmt.Println("\nDry run: nothing was changed.")
		return nil
	}

	if !*yes {
		fmt.Println()
		if permanent {
			ans, err := ask(fmt.Sprintf("Delete mode is PERMANENT. Type \"delete\" to remove %s for good: ", humanBytes(total)))
			if err != nil {
				return err
			}
			if ans != "delete" {
				fmt.Println("Cancelled.")
				return nil
			}
		} else {
			ok, err := confirm(fmt.Sprintf("Clean %d item(s) and free about %s?", len(selected), humanBytes(total)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("Cancelled.")
				return nil
			}
		}
	}

	res, err := cleaner.Clean(ctx, cfg, all, selected)
	for _, e := range res.Entries {
		loc := tildePath(e.Path)
		if loc == "" {
			loc = e.RuleID
		}
		if e.OK {
			fmt.Printf("  ✓ %s  %s\n", humanBytes(e.Bytes), loc)
		} else {
			fmt.Printf("  ✗ %s  %s: %s\n", humanBytes(e.Bytes), loc, e.Error)
		}
	}
	_ = reporter.AdjustLastScan(res.FreedBytes)
	fmt.Printf("\nFreed about %s.", humanBytes(res.FreedBytes))
	if res.Failed > 0 {
		fmt.Printf(" %d item(s) failed.", res.Failed)
	}
	if !permanent && res.FreedBytes > 0 {
		fmt.Print(" Items are in the Trash until you empty it.")
	}
	fmt.Println()
	if err != nil {
		return err
	}
	if res.Failed > 0 {
		return errors.New("some items could not be cleaned")
	}
	return nil
}

func actionFor(it scanner.Item, all []rules.Rule, permanent bool) string {
	if it.CleanMethod == rules.MethodCommand {
		for _, r := range all {
			if r.ID == it.RuleID && len(r.Clean.Commands) > 0 {
				cmd := strings.Join(r.Clean.Commands[0], " ")
				if len(r.Clean.Commands) > 1 {
					cmd += fmt.Sprintf(" (+%d)", len(r.Clean.Commands)-1)
				}
				return "run " + cmd
			}
		}
		return "run tool"
	}
	if permanent {
		return "delete permanently"
	}
	return "move to Trash"
}

func csvSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out[v] = true
		}
	}
	return out
}

func cmdLog(ctx context.Context, args []string) error {
	fs := newFlags("log", "[flags]")
	n := fs.Int("n", 30, "number of most recent entries to show (0 for all)")
	asJSON := fs.Bool("json", false, "print entries as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	entries, err := cleaner.ReadLog()
	if err != nil {
		return err
	}
	if *n > 0 && len(entries) > *n {
		entries = entries[len(entries)-*n:]
	}
	if *asJSON {
		return printJSON(entries)
	}
	if len(entries) == 0 {
		fmt.Println("No cleanups yet.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tRESULT\tSIZE\tMETHOD\tLOCATION")
	for _, e := range entries {
		result := "ok"
		if !e.OK {
			result = "failed: " + e.Error
		}
		loc := tildePath(e.Path)
		if loc == "" {
			loc = e.RuleID
		}
		if e.TrashedTo != "" && e.TrashedTo != "Trash" && e.TrashedTo != "Recycle Bin" {
			loc += " → " + tildePath(e.TrashedTo)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format("2006-01-02 15:04"), result, humanBytes(e.Bytes), e.Method, loc)
	}
	return tw.Flush()
}

func cmdRules(ctx context.Context, args []string) error {
	fs := newFlags("rules", "[flags]")
	asJSON := fs.Bool("json", false, "print rules as JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	all, err := loadRules()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(all)
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tCLEAN\tSTATUS\tSOURCE")
	for _, r := range all {
		status := "on"
		switch {
		case !r.AppliesToOS():
			status = "n/a on this OS"
		case !cfg.RuleEnabled(r.ID):
			status = "off"
		case !r.Available():
			status = r.Match.RequiresCommand + " not installed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Kind, r.Clean.Method, status, r.Source)
	}
	tw.Flush()
	dir, _ := config.Path("rules")
	fmt.Printf("\nTurn a rule off with: tidyfleet config set disabled_rules <id>[,<id>…]\nCustom rules: add *.yaml files to %s\n", tildePath(dir))
	return nil
}
