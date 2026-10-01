package health

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

func systemVolume() string {
	if d := os.Getenv("SystemDrive"); d != "" {
		return d + `\`
	}
	return `C:\`
}

func powershell(ctx context.Context, script string) (string, error) {
	out, err := output(ctx, 30*time.Second, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	return strings.TrimSpace(out), err
}

func collectPlatform(ctx context.Context, m *Metrics, opt Options) {
	m.OSName = "windows"

	full, err1 := powershell(ctx, "(Get-CimInstance -Namespace root/wmi -ClassName BatteryFullChargedCapacity | Select-Object -First 1).FullChargedCapacity")
	design, err2 := powershell(ctx, "(Get-CimInstance -Namespace root/wmi -ClassName BatteryStaticData | Select-Object -First 1).DesignedCapacity")
	if err1 == nil && err2 == nil && full != "" && design != "" {
		m.BatteryPresent = true
		f, _ := strconv.ParseFloat(full, 64)
		d, _ := strconv.ParseFloat(design, 64)
		if d > 0 && f > 0 {
			m.BatteryHealthPct = ptr(round1(f * 100 / d))
		}
		if c, err := powershell(ctx, "(Get-CimInstance -Namespace root/wmi -ClassName BatteryCycleCount | Select-Object -First 1).CycleCount"); err == nil {
			if n, err := strconv.Atoi(c); err == nil {
				m.BatteryCycles = ptr(n)
			}
		}
	}

	// Readable without admin rights: 1, 3 and 5 mean BitLocker protection is on.
	drive := strings.TrimSuffix(systemVolume(), `\`)
	if out, err := powershell(ctx, "(New-Object -ComObject Shell.Application).NameSpace('"+drive+"').Self.ExtendedProperty('System.Volume.BitLockerProtection')"); err == nil && out != "" {
		m.DiskEncrypted = ptr(out == "1" || out == "3" || out == "5")
	}
	if out, err := powershell(ctx, "(Get-NetFirewallProfile | Where-Object { -not $_.Enabled }).Count"); err == nil && out != "" {
		m.FirewallEnabled = ptr(out == "0")
	}
}
