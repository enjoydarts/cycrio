package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

type namedWorkflow struct {
	hatchet.WorkflowBase
	name string
}

func (w namedWorkflow) GetName() string { return w.name }

func TestWorkerGroupsReserveControlCapacity(t *testing.T) {
	workflow := func(name string) hatchet.WorkflowBase { return namedWorkflow{name: name} }
	groups := workerGroups("process", workerTasks{
		check: workflow("check"), catalog: workflow("catalog"), aiTest: workflow("ai-test"),
		mailProbe: workflow("probe"), mailDiscovery: workflow("discover"), mailProcessing: workflow("processing"),
	})
	if len(groups) != 2 {
		t.Fatalf("worker groups = %d, want separate control and processing workers", len(groups))
	}
	got := make(map[string][]string)
	for _, group := range groups {
		if group.slots != 2 {
			t.Fatalf("%s slots = %d, want 2", group.name, group.slots)
		}
		for _, task := range group.workflows {
			got[group.name] = append(got[group.name], task.GetName())
		}
	}
	want := map[string][]string{
		"process-control":    {"check", "catalog", "ai-test", "probe", "discover"},
		"process-processing": {"processing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow routing = %v, want %v", got, want)
	}
}

type lifecycleWorker struct {
	name       string
	events     *[]string
	startErr   error
	cleanupErr error
}

func (w lifecycleWorker) Start() (func() error, error) {
	*w.events = append(*w.events, "start "+w.name)
	if w.startErr != nil {
		return nil, w.startErr
	}
	return func() error {
		*w.events = append(*w.events, "stop "+w.name)
		return w.cleanupErr
	}, nil
}

func TestStartWorkerGroupsCleansUpOnRegistrationOrStartFailure(t *testing.T) {
	for _, stage := range []string{"register", "start"} {
		t.Run(stage, func(t *testing.T) {
			var events []string
			failure := errors.New("unavailable")
			_, err := startWorkerGroups([]workerGroup{{name: "control"}, {name: "processing"}}, func(group workerGroup) (workerStarter, error) {
				events = append(events, "register "+group.name)
				if group.name == "processing" && stage == "register" {
					return nil, failure
				}
				worker := lifecycleWorker{name: group.name, events: &events}
				if group.name == "processing" && stage == "start" {
					worker.startErr = failure
				}
				return worker, nil
			})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want startup failure", err)
			}
			want := []string{"register control", "start control", "register processing"}
			if stage == "start" {
				want = append(want, "start processing")
			}
			want = append(want, "stop control")
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
		})
	}
}

func TestStartWorkerGroupsCleanupAttemptsBothAndReportsFailure(t *testing.T) {
	var events []string
	failure := errors.New("cleanup unavailable")
	cleanup, err := startWorkerGroups([]workerGroup{{name: "control"}, {name: "processing"}}, func(group workerGroup) (workerStarter, error) {
		return lifecycleWorker{name: group.name, events: &events, cleanupErr: failure}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); !errors.Is(err, failure) {
		t.Fatalf("cleanup = %v, want failure", err)
	}
	want := []string{"start control", "start processing", "stop processing", "stop control"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestWorkerLifecycleErrorsDoNotExposeSDKDetails(t *testing.T) {
	for _, stage := range []string{"register", "start", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("private SDK details")
			var events []string
			cleanup, err := startWorkerGroups([]workerGroup{{name: "control"}}, func(group workerGroup) (workerStarter, error) {
				if stage == "register" {
					return nil, failure
				}
				worker := lifecycleWorker{name: group.name, events: &events}
				if stage == "start" {
					worker.startErr = failure
				}
				if stage == "cleanup" {
					worker.cleanupErr = failure
				}
				return worker, nil
			})
			if stage == "cleanup" {
				if err != nil {
					t.Fatal(err)
				}
				err = cleanup()
			}
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want preserved cause", err)
			}
			if strings.Contains(err.Error(), "private SDK details") {
				t.Fatalf("error exposes SDK details: %v", err)
			}
		})
	}
}
