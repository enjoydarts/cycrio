package infrastructure

import (
	"testing"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

func TestWorkerReadyRequiresOwnActiveRecentHeartbeat(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-5 * time.Second)
	stale := now.Add(-time.Minute)
	active := rest.WorkerStatus("ACTIVE")
	inactive := rest.WorkerStatus("INACTIVE")
	for _, test := range []struct {
		name   string
		worker rest.Worker
		want   bool
	}{
		{"own active", rest.Worker{Name: "mine", Status: &active, LastHeartbeatAt: &fresh}, true},
		{"other", rest.Worker{Name: "other", Status: &active, LastHeartbeatAt: &fresh}, false},
		{"stale", rest.Worker{Name: "mine", Status: &active, LastHeartbeatAt: &stale}, false},
		{"inactive", rest.Worker{Name: "mine", Status: &inactive, LastHeartbeatAt: &fresh}, false},
		{"unregistered", rest.Worker{Name: "mine"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkerReady("mine", []rest.Worker{test.worker}, now); got != test.want {
				t.Fatalf("ready = %v, want %v", got, test.want)
			}
		})
	}
}
