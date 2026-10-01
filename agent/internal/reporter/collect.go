package reporter

import (
	"context"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/health"
)

// updatesEvery limits the pending-updates check, which is slow and hits the network.
const updatesEvery = 12 * time.Hour

// Collect builds the snapshot that would be sent: live metrics, the last scan
// total, and the cached pending-updates count (refreshed when due or forced).
func Collect(ctx context.Context, agentVersion string, forceUpdates bool) health.Snapshot {
	uc := LoadUpdatesCheck()
	due := forceUpdates || time.Since(uc.CheckedAt) > updatesEvery
	snap := health.Collect(ctx, health.Options{
		CheckUpdates: due,
		Reclaimable:  ReclaimableForReport(),
		AgentVersion: agentVersion,
	})
	switch {
	case due && snap.Metrics.PendingUpdates != nil:
		_ = SaveUpdatesCheck(UpdatesCheck{CheckedAt: time.Now().UTC(), Pending: snap.Metrics.PendingUpdates})
	case snap.Metrics.PendingUpdates == nil:
		snap.Metrics.PendingUpdates = uc.Pending
	}
	return snap
}
