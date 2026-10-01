package scanner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/fsutil"
)

type probeResult struct {
	bytes  int64
	detail string
}

var probes = map[string]func(ctx context.Context) (probeResult, error){
	"docker":             probeDocker,
	"simctl-unavailable": probeSimctl,
}

func probeDocker(ctx context.Context) (probeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{json .}}").Output()
	if err != nil {
		return probeResult{}, fmt.Errorf("docker is not running")
	}
	var res probeResult
	var parts []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var row struct{ Type, Reclaimable string }
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		if row.Type != "Images" && row.Type != "Build Cache" {
			continue // containers and volumes may hold data; never touched
		}
		n := parseDockerSize(row.Reclaimable)
		res.bytes += n
		if f := strings.Fields(row.Reclaimable); len(f) > 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", strings.ToLower(row.Type), f[0]))
		}
	}
	res.detail = strings.Join(parts, ", ")
	return res, nil
}

var dockerSizeRe = regexp.MustCompile(`^([\d.]+)\s*([kKMGT]?B)`)

// parseDockerSize parses docker's decimal sizes such as "1.23GB (45%)".
func parseDockerSize(s string) int64 {
	m := dockerSizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}[m[2]]
	return int64(f * mult)
}

func probeSimctl(ctx context.Context) (probeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "unavailable", "-j").Output()
	if err != nil {
		return probeResult{}, fmt.Errorf("simctl unavailable")
	}
	var parsed struct {
		Devices map[string][]struct {
			DataPath string `json:"dataPath"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return probeResult{}, err
	}
	var res probeResult
	count := 0
	for _, list := range parsed.Devices {
		for _, d := range list {
			count++
			if d.DataPath != "" {
				res.bytes += fsutil.DiskUsage(filepath.Dir(d.DataPath)).Freeable
			}
		}
	}
	res.detail = fmt.Sprintf("%d unavailable simulators", count)
	return res, nil
}
