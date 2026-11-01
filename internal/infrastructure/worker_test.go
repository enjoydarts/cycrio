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

func TestWorkersReadyRequiresEveryWorkerActiveAndFresh(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-5 * time.Second)
	stale := now.Add(-30 * time.Second)
	future := now.Add(time.Second)
	active := rest.WorkerStatus("ACTIVE")
	inactive := rest.WorkerStatus("INACTIVE")
	for _, test := range []struct {
		name   string
		second *rest.Worker
		want   bool
	}{
		{"both ready", &rest.Worker{Name: "processing", Status: &active, LastHeartbeatAt: &fresh}, true},
		{"missing processing", nil, false},
		{"inactive processing", &rest.Worker{Name: "processing", Status: &inactive, LastHeartbeatAt: &fresh}, false},
		{"stale processing", &rest.Worker{Name: "processing", Status: &active, LastHeartbeatAt: &stale}, false},
		{"future heartbeat", &rest.Worker{Name: "processing", Status: &active, LastHeartbeatAt: &future}, false},
		{"missing heartbeat", &rest.Worker{Name: "processing", Status: &active}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			workers := []rest.Worker{{Name: "control", Status: &active, LastHeartbeatAt: &fresh}}
			if test.second != nil {
				workers = append(workers, *test.second)
			}
			if got := WorkersReady([]string{"control", "processing"}, workers, now); got != test.want {
				t.Fatalf("ready = %v, want %v", got, test.want)
			}
			if got := WorkersReady([]string{"processing", "control"}, workers, now); got != test.want {
				t.Fatalf("reversed ready = %v, want %v", got, test.want)
			}
		})
	}
	if WorkersReady(nil, nil, now) {
		t.Fatal("no registered workers must not be ready")
	}
}
