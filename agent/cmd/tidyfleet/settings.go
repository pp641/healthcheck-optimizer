package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
)

func cmdConfig(ctx context.Context, args []string) error {
	fs := newFlags("config", "show | set <key> <value> | path")
	asJSON := fs.Bool("json", false, "print settings as JSON (show only)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	sub := "show"
	if len(pos) > 0 {
		sub = pos[0]
	}
	switch sub {
	case "show":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(cfg)
		}
		return showConfig(cfg)
	case "set":
		if len(pos) != 3 {
			return errors.New("usage: tidyfleet config set <key> <value>  (lists are comma-separated)")
		}
		return setConfig(pos[1], pos[2])
	case "path":
		d, err := config.Dir()
		if err != nil {
			return err
		}
		fmt.Println(d)
		return nil
	default:
		return fmt.Errorf("unknown config subcommand %q (show, set, path)", sub)
	}
}

func setConfig(key, value string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if key == "ai_mode" && value != "off" {
		if e, err := reporter.LoadEnrollment(); err == nil && e != nil && !e.Policy.AllowAI {
			return fmt.Errorf("ai_mode is managed by %s, which has turned the AI assistant off", e.OrgName)
		}
	}
	if err := cfg.Set(key, value); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("%s updated.\n", key)
	return nil
}

func showConfig(cfg config.Config) error {
	e, err := reporter.LoadEnrollment()
	if err != nil {
		return err
	}
	list := func(v []string) string {
		if len(v) == 0 {
			return "(none)"
		}
		out := make([]string, len(v))
		for i, p := range v {
			out[i] = tildePath(p)
		}
		return strings.Join(out, ", ")
	}
	aiMode := cfg.AIMode
	if e != nil && !e.Policy.AllowAI {
		aiMode = "off (managed by your organization)"
	}
	fmt.Println("Cleaner settings (only you can change these):")
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  scan_roots\t%s\n", list(cfg.ScanRoots))
	fmt.Fprintf(tw, "  exclude\t%s\n", list(cfg.Exclude))
	fmt.Fprintf(tw, "  disabled_rules\t%s\n", list(cfg.DisabledRules))
	fmt.Fprintf(tw, "  stale_days\t%d\n", cfg.StaleDays)
	fmt.Fprintf(tw, "  delete_mode\t%s\n", cfg.DeleteMode)
	fmt.Fprintf(tw, "  always_preview\t%t\n", cfg.AlwaysPreview)
	fmt.Fprintf(tw, "  clean_dirty_repos\t%t\n", cfg.CleanDirtyRepos)
	fmt.Fprintf(tw, "  max_depth\t%d\n", cfg.MaxDepth)
	fmt.Fprintf(tw, "  schedule\t%s\n", cfg.Schedule)
	fmt.Fprintf(tw, "  ai_mode\t%s\n", aiMode)
	tw.Flush()

	fmt.Println()
	if e == nil {
		fmt.Println("Organization: not enrolled (nothing is reported anywhere).")
		return nil
	}
	fmt.Printf("Health reporting (managed by %s):\n", e.OrgName)
	tw = tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  report interval\t%s\n", e.Policy.Interval())
	fmt.Fprintf(tw, "  AI assistant allowed\t%t\n", e.Policy.AllowAI)
	fmt.Fprintf(tw, "  server\t%s\n", e.ServerURL)
	return tw.Flush()
}
