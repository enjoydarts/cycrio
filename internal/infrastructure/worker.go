package infrastructure

import (
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

// WorkersReady requires every worker in this process to be ready.
func WorkersReady(names []string, workers []rest.Worker, now time.Time) bool {
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if !WorkerReady(name, workers, now) {
			return false
		}
	}
	return true
}

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
