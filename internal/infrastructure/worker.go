package infrastructure

import (
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

// WorkerReady requires Hatchet to observe a recent heartbeat from this process.
func WorkerReady(name string, workers []rest.Worker, now time.Time) bool {
	for _, worker := range workers {
		if worker.Name == name && worker.Status != nil && string(*worker.Status) == "ACTIVE" && worker.LastHeartbeatAt != nil {
			age := now.Sub(*worker.LastHeartbeatAt)
			return age >= 0 && age < 30*time.Second
		}
	}
	return false
}
