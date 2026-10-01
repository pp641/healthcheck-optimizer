package health

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// Options tweak collection. Pending-update checks can take a minute (they hit
// the network), so callers decide whether to run them.
type Options struct {
	CheckUpdates bool
	Reclaimable  *int64
	AgentVersion string
}

func Collect(ctx context.Context, opt Options) Snapshot {
	m := Metrics{OSName: runtime.GOOS, ReclaimableBytes: opt.Reclaimable, AgentVersion: opt.AgentVersion}

	if u, err := disk.UsageWithContext(ctx, systemVolume()); err == nil {
		m.DiskTotalBytes, m.DiskFreeBytes = u.Total, u.Free
		m.DiskUsedPct = round1(u.UsedPercent)
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemTotalBytes, m.MemUsedPct = v.Total, round1(v.UsedPercent)
	}
	if p, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(p) > 0 {
		m.CPULoadPct = round1(p[0])
	}
	if info, err := host.InfoWithContext(ctx); err == nil {
		m.UptimeSeconds = info.Uptime
		m.OSName = info.Platform
		m.OSVersion = info.PlatformVersion
		m.OSMajor, _ = strconv.Atoi(strings.SplitN(info.PlatformVersion, ".", 2)[0])
	}
	collectPlatform(ctx, &m, opt)
	return Snapshot{TakenAt: time.Now().UTC(), Metrics: m}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func ptr[T any](v T) *T { return &v }

func output(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(b), err
}
