package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/tidyfleet/tidyfleet/agent/internal/health"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
)

func cmdHealth(ctx context.Context, args []string) error {
	fs := newFlags("health", "[flags]")
	asJSON := fs.Bool("json", false, "print the snapshot exactly as it would be sent")
	updates := fs.Bool("updates", false, "check for pending OS updates now (slow)")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	snap := reporter.Collect(ctx, version, *updates)
	if *asJSON {
		return printJSON(snap)
	}
	printSnapshot(snap)
	return nil
}

func printSnapshot(s health.Snapshot) {
	m := s.Metrics
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "  %s\t%s\n", k, v) }
	row("Disk", fmt.Sprintf("%.1f%% used, %s free of %s", m.DiskUsedPct, humanBytes(int64(m.DiskFreeBytes)), humanBytes(int64(m.DiskTotalBytes))))
	if m.ReclaimableBytes != nil {
		row("Reclaimable dev space", humanBytes(*m.ReclaimableBytes))
	} else {
		row("Reclaimable dev space", "unknown (run `tidyfleet scan`)")
	}
	row("Memory", fmt.Sprintf("%.1f%% used of %s", m.MemUsedPct, humanBytes(int64(m.MemTotalBytes))))
	row("CPU load", fmt.Sprintf("%.1f%%", m.CPULoadPct))
	row("Uptime", fmt.Sprintf("%dd %dh", m.UptimeSeconds/86400, m.UptimeSeconds%86400/3600))
	switch {
	case !m.BatteryPresent:
		row("Battery", "none")
	default:
		v := "present"
		if m.BatteryHealthPct != nil {
			v = fmt.Sprintf("%.0f%% health", *m.BatteryHealthPct)
		}
		if m.BatteryCycles != nil {
			v += fmt.Sprintf(", %d cycles", *m.BatteryCycles)
		}
		row("Battery", v)
	}
	row("OS", m.OSName+" "+m.OSVersion)
	row("Pending updates", optInt(m.PendingUpdates, "not checked (use --updates)"))
	row("Disk encryption", optBool(m.DiskEncrypted, "on", "OFF"))
	row("Firewall", optBool(m.FirewallEnabled, "on", "off"))
	tw.Flush()
}

func optInt(v *int, unknown string) string {
	if v == nil {
		return unknown
	}
	return fmt.Sprint(*v)
}

func optBool(v *bool, yes, no string) string {
	switch {
	case v == nil:
		return "unknown"
	case *v:
		return yes
	default:
		return no
	}
}

func cmdEnroll(ctx context.Context, args []string) error {
	fs := newFlags("enroll", "<server-url> <enrollment-code>")
	yes := fs.Bool("yes", false, "enroll without asking for confirmation")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		fs.Usage()
		return errors.New("need a server URL and an enrollment code")
	}
	fmt.Println(`Enrolling shares laptop health totals with your organization:
  disk used/free, reclaimable dev space (one number), memory, CPU load, uptime,
  battery health, OS version, pending updates, disk encryption and firewall status.

It never shares file or folder names, paths, the disk tree, or your cleanup history.
Cleaner settings stay yours. You can see what was sent with ` + "`tidyfleet shared`" + `
and leave at any time with ` + "`tidyfleet leave`" + `.`)
	if !*yes {
		ok, err := confirm("\nEnroll this laptop?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	snap := reporter.Collect(ctx, version, false)
	e, err := reporter.Enroll(ctx, pos[0], pos[1], snap.Metrics.OSVersion, version)
	if err != nil {
		return err
	}
	fmt.Printf("\nEnrolled with %s.\n", e.OrgName)
	if res, err := reporter.Report(ctx, e, snap); err != nil {
		fmt.Printf("First snapshot is queued and will be sent later (%v).\n", err)
	} else if res.Sent > 0 {
		fmt.Println("First health snapshot sent.")
	}
	fmt.Println("Keep reporting on with the background agent (`tidyfleet daemon`, installed as a LaunchAgent on macOS).")
	return nil
}

func cmdReport(ctx context.Context, args []string) error {
	fs := newFlags("report", "[flags]")
	updates := fs.Bool("updates", false, "check for pending OS updates now (slow)")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	e, err := reporter.LoadEnrollment()
	if err != nil {
		return err
	}
	if e == nil {
		return errors.New("this device is not enrolled; nothing is reported")
	}
	res, err := reporter.Report(ctx, e, reporter.Collect(ctx, version, *updates))
	if err != nil {
		if errors.Is(err, reporter.ErrDeviceRemoved) {
			return fmt.Errorf("%w; local enrollment was cleared", err)
		}
		return fmt.Errorf("snapshot queued, send failed: %w (%d waiting)", err, res.Pending)
	}
	fmt.Printf("Sent %d snapshot(s) to %s.", res.Sent, e.OrgName)
	if res.Dropped > 0 {
		fmt.Printf(" %d rejected by the server were dropped.", res.Dropped)
	}
	fmt.Println(" See them with `tidyfleet shared`.")
	return nil
}

func cmdShared(ctx context.Context, args []string) error {
	fs := newFlags("shared", "[flags]")
	asJSON := fs.Bool("json", false, "print the raw snapshot only")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	e, err := reporter.LoadEnrollment()
	if err != nil {
		return err
	}
	rec, err := reporter.LastShared()
	if err != nil {
		return err
	}
	if rec == nil {
		if e == nil {
			fmt.Println("Not enrolled: nothing has been shared with anyone.")
		} else {
			fmt.Printf("Enrolled with %s, but nothing has been sent yet.\n", e.OrgName)
		}
		return nil
	}
	if *asJSON {
		_, err := os.Stdout.Write(append(rec.Snapshot, '\n'))
		return err
	}
	fmt.Printf("Last shared with %s (%s) at %s.\n", rec.OrgName, rec.ServerURL, rec.SentAt.Local().Format("2006-01-02 15:04"))
	fmt.Println("This is the complete data that was sent:")
	var pretty any
	if json.Unmarshal(rec.Snapshot, &pretty) == nil {
		printJSON(pretty)
	}
	if n := reporter.QueueLen(); n > 0 {
		fmt.Printf("\n%d snapshot(s) are waiting to be sent.\n", n)
	}
	return nil
}

func cmdLeave(ctx context.Context, args []string) error {
	fs := newFlags("leave", "[flags]")
	yes := fs.Bool("yes", false, "leave without asking for confirmation")
	force := fs.Bool("force", false, "clear local enrollment even if the server cannot be reached")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	e, err := reporter.LoadEnrollment()
	if err != nil {
		return err
	}
	if e == nil {
		fmt.Println("This device is not enrolled.")
		return nil
	}
	if !*yes {
		ok, err := confirm(fmt.Sprintf("Leave %s? Health reporting stops and the organization's copy of this device's history is deleted.", e.OrgName))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	if err := reporter.Leave(ctx, *force); err != nil {
		return err
	}
	fmt.Printf("Left %s. Nothing is reported anymore; the cleaner keeps working as before.\n", e.OrgName)
	return nil
}
