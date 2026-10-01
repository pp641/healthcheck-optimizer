package health

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// On APFS the root volume is the sealed system snapshot; user data lives on
// the Data volume, which is what fills up.
func systemVolume() string {
	if _, err := os.Stat("/System/Volumes/Data"); err == nil {
		return "/System/Volumes/Data"
	}
	return "/"
}

var ioregNum = regexp.MustCompile(`"(\w+)" = (\d+)`)

func collectPlatform(ctx context.Context, m *Metrics, opt Options) {
	m.OSName = "macos"

	if out, err := output(ctx, 10*time.Second, "ioreg", "-rn", "AppleSmartBattery"); err == nil && strings.TrimSpace(out) != "" {
		vals := map[string]int{}
		for _, match := range ioregNum.FindAllStringSubmatch(out, -1) {
			if _, seen := vals[match[1]]; !seen {
				vals[match[1]], _ = strconv.Atoi(match[2])
			}
		}
		m.BatteryPresent = true
		if c, ok := vals["CycleCount"]; ok {
			m.BatteryCycles = ptr(c)
		}
		maxCap := vals["AppleRawMaxCapacity"]
		if maxCap == 0 {
			maxCap = vals["NominalChargeCapacity"]
		}
		if design := vals["DesignCapacity"]; design > 0 && maxCap > 0 {
			m.BatteryHealthPct = ptr(round1(float64(maxCap) * 100 / float64(design)))
		}
	}

	if out, err := output(ctx, 10*time.Second, "fdesetup", "status"); err == nil {
		m.DiskEncrypted = ptr(strings.Contains(out, "FileVault is On"))
	}
	if out, err := output(ctx, 10*time.Second, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate"); err == nil {
		m.FirewallEnabled = ptr(strings.Contains(out, "is enabled") || strings.Contains(out, "State = 1") || strings.Contains(out, "State = 2"))
	}
	if opt.CheckUpdates {
		if out, err := output(ctx, 3*time.Minute, "softwareupdate", "--list"); err == nil {
			m.PendingUpdates = ptr(strings.Count(out, "* Label:"))
		}
	}
}
