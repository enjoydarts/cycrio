//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"testing"
)

func TestHatchetInterruptions(t *testing.T) {
	if *hatchetEvaluationDirectory == "" || *hatchetEvaluationDatabase == "" {
		t.Fatal("interruption evaluation requires explicit local flags")
	}
	reports := []hatchetInterruptionCase{}
	for _, kind := range []string{"cancel", "disconnect", "user_deleted", "worker_crash"} {
		t.Run(kind, func(t *testing.T) {
			report := runHatchetInterruption(t, kind, *hatchetEvaluationDirectory, *hatchetEvaluationDatabase)
			if report.Calls != 1 || report.TransportRuns != 2 || report.ResultState != "unknown" {
				t.Fatal("unexpected interrupted processing outcome")
			}
			if kind == "worker_crash" {
				if report.UnknownCalls != 1 || report.KnownAmount != nil || report.StopMethod != "sigkill_child_process" {
					t.Fatal("crash lost unknown cost or process stop evidence")
				}
			} else if report.UnknownCalls != 0 || report.KnownAmount == nil || *report.KnownAmount != "0.001" {
				t.Fatal("late synthetic cost discarded")
			}
			reports = append(reports, report)
		})
	}
	if t.Failed() {
		return
	}
	writeHatchetInterruptionReport(t, *hatchetEvaluationDirectory, reports)
}
