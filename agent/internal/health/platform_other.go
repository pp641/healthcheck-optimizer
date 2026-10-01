//go:build !darwin && !windows

package health

import "context"

func systemVolume() string { return "/" }

func collectPlatform(ctx context.Context, m *Metrics, opt Options) {}
