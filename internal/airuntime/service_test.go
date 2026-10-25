package airuntime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
)

const testID = "11111111-1111-4111-8111-111111111111"

type serviceRepo struct {
	Repository
	execution   Execution
	begins      []BeginCallInput
	updates     []CallUpdate
	completions []TestCompletion
	events      *[]string
	finishErr   error
	checkCount  int
	revokeAt    int
	checkErr    error
	callRecord  *Call
}

func (r *serviceRepo) StartTest(context.Context, Lease) (Execution, error) { return r.execution, nil }
func (r *serviceRepo) CheckExecution(context.Context, Execution, Lease) error {
	r.checkCount++
	if r.checkErr != nil {
		return r.checkErr
	}
	if r.revokeAt > 0 && r.checkCount >= r.revokeAt {
		return ErrConflict
	}
	return nil
}
func (r *serviceRepo) GetCall(context.Context, string) (Call, error) {
	if r.callRecord == nil {
		return Call{}, ErrNotFound
	}
	return *r.callRecord, nil
}
func (r *serviceRepo) BeginCall(_ context.Context, in BeginCallInput) (Call, error) {
	r.begins = append(r.begins, in)
	*r.events = append(*r.events, "begin:"+in.Stage)
	return Call{ID: testID}, nil
}
func (r *serviceRepo) FinishCall(_ context.Context, _ string, in CallUpdate) (Call, error) {
	r.updates = append(r.updates, in)
	*r.events = append(*r.events, "record:"+in.Outcome)
	return Call{}, r.finishErr
}
func (r *serviceRepo) FinishTest(_ context.Context, _ Lease, in TestCompletion) error {
	r.completions = append(r.completions, in)
	return nil
}

type serviceClient struct {
	requests []ai.Request
	events   *[]string
	fail     bool
}

func (c *serviceClient) Do(_ context.Context, in ai.Request) (ai.Response, error) {
	c.requests = append(c.requests, in)
	*c.events = append(*c.events, "send:"+in.Provider)
	amount, currency := "0", "USD"
	out := ai.Response{Usage: ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &currency, Reason: "reported"}}
	if c.fail {
		return out, &ai.Error{Code: "invalid_output", HTTPStatus: 200}
	}
	if in.Provider == "liquid" {
		out.Classification = &ai.Classification{Label: "subscription_related"}
	} else {
		out.Extraction = &ai.Extraction{Events: []ai.Event{{Service: "Cycrio Test", EventType: "payment"}}}
	}
	return out, nil
}
func (c *serviceClient) Reconcile(context.Context, ai.ReconcileRequest) (ai.Reconciliation, error) {
	return ai.Reconciliation{}, nil
}
func runtimeServiceFixture() (*Service, *serviceRepo, *serviceClient, *[]string) {
	events := []string{}
	repo := &serviceRepo{execution: Execution{Run: TestRun{ID: testID, ProfileID: testID}, ActorID: testID, Profile: Profile{ID: testID, Primary: ModelRef{ID: testID, Provider: "liquid", ModelID: "d1", CredentialID: testID}, Secondary: ModelRef{ID: testID, Provider: "openai", ModelID: "synthetic", CredentialID: testID}, MaxInputBytes: 16384, MaxOutputTokens: 1024, TimeoutSeconds: 60}}, events: &events}
	client := &serviceClient{events: &events}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key-not-for-persistence"), nil })
	return service, repo, client, &events
}
func TestConnectionTestRecordsBeforeEveryCallAndKeepsSecretsTransient(t *testing.T) {
	service, repo, client, events := runtimeServiceFixture()
	if err := service.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*events, []string{"begin:classification", "send:liquid", "record:success", "begin:extraction", "send:openai", "record:success"}) {
		t.Fatal(*events)
	}
	if len(repo.completions) != 1 || repo.completions[0].State != "succeeded" || *repo.completions[0].ExtractedEvents != 1 {
		t.Fatal(repo.completions)
	}
	for _, in := range repo.begins {
		if in.Scope != "admin_test" || in.ConsumingUserID != nil || in.InitiatedByUserID == nil {
			t.Fatal("misattributed test", in)
		}
	}
	for _, req := range client.requests {
		for _, b := range req.Key {
			if b != 0 {
				t.Fatal("key was not discarded")
			}
		}
	}
}
func TestInvalidOutputStillRecordsReportedZeroCostWithoutRetry(t *testing.T) {
	service, repo, client, _ := runtimeServiceFixture()
	client.fail = true
	if err := service.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || len(repo.updates) != 1 || repo.updates[0].Outcome != "invalid_output" || repo.updates[0].Cost.Amount == nil || *repo.updates[0].Cost.Amount != "0" {
		t.Fatal(repo.updates)
	}
	if len(repo.completions) != 1 || repo.completions[0].State != "failed" {
		t.Fatal(repo.completions)
	}
}
func TestRecordingFailureDoesNotRepeatAnInference(t *testing.T) {
	service, repo, client, _ := runtimeServiceFixture()
	repo.finishErr = ErrUnavailable
	if err := service.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || len(repo.completions) != 1 || repo.completions[0].State != "unknown" {
		t.Fatal(len(client.requests), repo.completions)
	}
}
func TestExecutionRevocationStopsBeforeTheNextProviderCall(t *testing.T) {
	service, repo, client, _ := runtimeServiceFixture()
	repo.revokeAt = 2
	if err := service.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || len(repo.completions) != 1 || repo.completions[0].State != "failed" {
		t.Fatal(len(client.requests), repo.completions)
	}
}
func TestTransientStorageFailuresAreNotReportedAsMissingConfiguration(t *testing.T) {
	for _, where := range []string{"execution", "credentials"} {
		service, repo, client, _ := runtimeServiceFixture()
		if where == "execution" {
			repo.checkErr = ErrUnavailable
		} else {
			service.resolve = func(context.Context, string) ([]byte, error) { return nil, ErrUnavailable }
		}
		if err := service.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID}); !errors.Is(err, ErrUnavailable) {
			t.Fatal(where, "storage failure was swallowed", err)
		}
		if len(client.requests) != 0 || len(repo.completions) != 1 || repo.completions[0].State != "unknown" {
			t.Fatal(where, repo.completions)
		}
	}
}
func TestReconcileDistinguishesCredentialRevocationFromStorageFailure(t *testing.T) {
	service, repo, _, _ := runtimeServiceFixture()
	generation := "gen-synthetic"
	repo.callRecord = &Call{ID: testID, Provider: "openrouter", GenerationID: &generation, CredentialID: testID}
	for _, known := range []error{ErrUnavailable, ErrCredentialsRequired} {
		service.resolve = func(context.Context, string) ([]byte, error) { return nil, known }
		if _, err := service.Reconcile(context.Background(), testID, testID); !errors.Is(err, known) {
			t.Fatal("wrong credential error", known, err)
		}
	}
}
