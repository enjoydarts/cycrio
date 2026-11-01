package main

import (
	"errors"
	"fmt"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

type workerTasks struct {
	check, catalog, priceResearch, aiTest, mailProbe, mailDiscovery, mailProcessing hatchet.WorkflowBase
}

type workerGroup struct {
	name      string
	slots     int
	workflows []hatchet.WorkflowBase
}

// Processing keeps its existing two slots (at most four LLM calls per batch).
// Separate control slots allow discovery and probes to run during long batches.
func workerGroups(name string, tasks workerTasks) []workerGroup {
	return []workerGroup{
		{name: name + "-control", slots: 2, workflows: []hatchet.WorkflowBase{tasks.check, tasks.aiTest, tasks.mailProbe, tasks.mailDiscovery}},
		{name: name + "-processing", slots: 2, workflows: []hatchet.WorkflowBase{tasks.mailProcessing}},
		{name: name + "-maintenance", slots: 1, workflows: []hatchet.WorkflowBase{tasks.catalog, tasks.priceResearch}},
	}
}

type workerStarter interface {
	Start() (func() error, error)
}

// Preserve causes for inspection without exposing SDK details through main's log.
type workerLifecycleError struct {
	operation string
	name      string
	cause     error
}

func (e workerLifecycleError) Error() string {
	return fmt.Sprintf("Hatchet worker %s failed (%s)", e.operation, e.name)
}

func (e workerLifecycleError) Unwrap() error { return e.cause }

func startWorkerGroups(groups []workerGroup, register func(workerGroup) (workerStarter, error)) (func() error, error) {
	var cleanups []func() error
	cleanup := func() error {
		var result error
		for i := len(cleanups) - 1; i >= 0; i-- {
			result = errors.Join(result, cleanups[i]())
		}
		return result
	}
	for _, group := range groups {
		worker, err := register(group)
		if err != nil {
			return nil, errors.Join(workerLifecycleError{operation: "registration", name: group.name, cause: err}, cleanup())
		}
		stop, err := worker.Start()
		if err != nil {
			return nil, errors.Join(workerLifecycleError{operation: "start", name: group.name, cause: err}, cleanup())
		}
		cleanups = append(cleanups, func() error {
			if err := stop(); err != nil {
				return workerLifecycleError{operation: "cleanup", name: group.name, cause: err}
			}
			return nil
		})
	}
	return cleanup, nil
}
