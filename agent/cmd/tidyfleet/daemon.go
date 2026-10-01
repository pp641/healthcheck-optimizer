package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
)

const (
	tick          = time.Minute
	retryEvery    = 5 * time.Minute
	scanEvery     = 24 * time.Hour
	remindEvery   = 7 * 24 * time.Hour
	remindMinimum = 1_000_000_000 // don't nag about less than 1 GB
)

type reminderState struct {
	LastNotified time.Time `json:"last_notified"`
}

// cmdDaemon runs in the background. It never deletes anything: it only
// reports health (when enrolled) and refreshes the read-only scan total.
func cmdDaemon(ctx context.Context, args []string) error {
	fs := newFlags("daemon", "")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags)
	log.Printf("tidyfleet %s daemon started", version)

	var nextReport, nextRetry time.Time
	for {
		now := time.Now()
		cfg, err := config.Load()
		if err != nil {
			log.Printf("config: %v", err)
		}
		e, err := reporter.LoadEnrollment()
		if err != nil {
			log.Printf("enrollment: %v", err)
		}

		if e != nil || cfg.Schedule == "weekly" {
			if last, _ := reporter.LastScan(); last == nil || now.Sub(last.ScannedAt) > scanEvery {
				if _, _, rep, err := runScan(ctx, true); err == nil {
					log.Printf("scan: %s reclaimable in %d items", humanBytes(rep.ReclaimableBytes), len(rep.Items))
				}
			}
		}
		if cfg.Schedule == "weekly" {
			maybeRemind(now)
		}

		if e != nil {
			switch {
			case !now.Before(nextReport):
				res, err := reporter.Report(ctx, e, reporter.Collect(ctx, version, false))
				logReport(res, err)
				nextReport = now.Add(e.Policy.Interval())
				nextRetry = now.Add(retryEvery)
			case !now.Before(nextRetry) && reporter.QueueLen() > 0:
				res, err := reporter.Flush(ctx, e)
				logReport(res, err)
				nextRetry = now.Add(retryEvery)
			}
		} else {
			nextReport = time.Time{} // report right away after enrolling
		}

		select {
		case <-ctx.Done():
			log.Print("daemon stopped")
			return nil
		case <-time.After(tick):
		}
	}
}

func logReport(res reporter.FlushResult, err error) {
	switch {
	case errors.Is(err, reporter.ErrDeviceRemoved):
		log.Printf("report: %v; stopped reporting", err)
	case err != nil:
		log.Printf("report: %v (%d queued)", err, res.Pending)
	default:
		log.Printf("report: sent %d snapshot(s), dropped %d", res.Sent, res.Dropped)
	}
}

func maybeRemind(now time.Time) {
	p, err := config.Path("reminder.json")
	if err != nil {
		return
	}
	var st reminderState
	_ = config.ReadJSON(p, &st)
	if now.Sub(st.LastNotified) < remindEvery {
		return
	}
	last, _ := reporter.LastScan()
	if last == nil || last.ReclaimableBytes < remindMinimum {
		return
	}
	msg := fmt.Sprintf("You can reclaim %s of old build output. Run `tidyfleet clean` to review it.", humanBytes(last.ReclaimableBytes))
	if err := notify("Tidyfleet", msg); err != nil {
		log.Printf("reminder: %v", err)
	}
	log.Printf("reminder: %s", msg)
	st.LastNotified = now
	_ = config.WriteJSON(p, st)
}

func notify(title, msg string) error {
	switch runtime.GOOS {
	case "darwin":
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		return exec.Command("osascript", "-e", "display notification "+q(msg)+" with title "+q(title)).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command("notify-send", title, msg).Run()
		}
	}
	fmt.Fprintln(os.Stderr, title+": "+msg)
	return nil
}
