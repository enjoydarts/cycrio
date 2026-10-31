package mailprocessing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/subscriptionprompts"
	"io"
	"time"
	"unicode/utf8"
)

const classifierInstructions = subscriptionprompts.ClassifierInstructions
const extractorInstructions = subscriptionprompts.ExtractorInstructions

func (s *Service) Execute(ctx context.Context, l Lease) (err error) {
	if !ValidID(l.RunID) || !ValidID(l.LeaseID) {
		return ErrInvalid
	}
	w, e := s.repo.Start(ctx, l)
	if errors.Is(e, ErrNotFound) || errors.Is(e, ErrConflict) {
		return nil
	}
	if e != nil {
		return safe(e)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	completion := Completion{State: "failed", Events: []ai.Event{}}
	// All adapter panics are reduced to fixed metadata before crossing the worker boundary.
	defer func() {
		if recover() != nil {
			completion = Completion{State: "unknown", ErrorCode: "execution_panic", Events: []ai.Event{}}
		}
		finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if e := s.repo.Finish(finish, l, completion); e != nil {
			err = ErrUnavailable
		}
	}()
	if s.keys == nil {
		completion.ErrorCode = "credentials_unavailable"
		return nil
	}
	api := mailsync.APIProvider(w.Credential.Provider)
	var raw []byte
	if !api {
		raw, e = s.keys.Open(w.Credential.Envelope, mailaccounts.CredentialAAD(w.Credential))
	}
	defer clear(raw)
	var credential struct{ Login, Password []byte }
	var decoded error
	if !api {
		decoded = json.Unmarshal(raw, &credential)
	}
	defer clear(credential.Login)
	defer clear(credential.Password)
	if e != nil || decoded != nil || !api && (len(credential.Login) == 0 || len(credential.Password) == 0) {
		completion.ErrorCode = "credentials_unavailable"
		return nil
	}
	aad := mailsync.MessageAAD(mailsync.Work{OwnerID: w.OwnerID, Run: mailsync.Run{AccountID: w.Run.AccountID}, FolderID: w.FolderID}, w.MessageID)
	sourceBytes, e := s.keys.Open(w.Envelope, aad)
	defer clear(sourceBytes)
	var validity, uid uint32
	var remoteID string
	var valid bool
	if api {
		remoteID, valid = decodeRemoteSourceReference(sourceBytes, w.Credential.Provider)
	} else {
		validity, uid, valid = decodeSourceReference(sourceBytes)
	}
	if e != nil || !valid {
		completion.ErrorCode = "source_missing"
		return nil
	}
	maximum := min(w.Profile.MaxInputBytes, mailcontent.MaxTextBytes)
	if maximum < 1 || maximum > 65536 {
		completion.ErrorCode = "configuration_unavailable"
		return nil
	}
	if w.FullEmail {
		maximum = mailcontent.MaxWireBytes
	}
	reader := s.reader
	if s.factory != nil {
		reader, e = s.factory(ctx, w.Credential)
	} else if api {
		reader = nil
	}
	if e != nil {
		completion.ErrorCode = SourceErrorCode(e)
		return nil
	}
	if reader == nil {
		completion.ErrorCode = "connection_failed"
		return nil
	}
	readCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	text, e := reader.ReadText(readCtx, string(credential.Login), credential.Password, mailcontent.Source{RemoteID: remoteID, Folder: w.FolderName, AllowedFolders: w.ReaderFolders, UIDValidity: validity, UID: uid, MaxTextBytes: maximum, FullEmail: w.FullEmail, StrictGeneration: w.Automatic})
	defer clear(text)
	if e == nil {
		e = readCtx.Err()
	}
	stop()
	if e != nil {
		completion.ErrorCode = SourceErrorCode(e)
		return nil
	}
	if len(text) > maximum {
		completion.ErrorCode = "input_too_large"
		return nil
	}
	if !utf8.Valid(text) {
		completion.ErrorCode = "invalid_response"
		return nil
	}
	first, outcome, e := s.call(ctx, l, w, "classification", w.Profile.Primary, classifierInstructions, text)
	if e != nil {
		completion.State = "unknown"
		completion.ErrorCode = "recording_failed"
		return ErrUnavailable
	}
	if outcome != "success" {
		completion.ErrorCode = outcome
		if ambiguous(outcome) {
			completion.State = "unknown"
		}
		return nil
	}
	label := first.Classification.Label
	completion.Classification = &label
	if label == "not_related" {
		completion.State = "not_related"
		return nil
	}
	second, outcome, e := s.call(ctx, l, w, "extraction", w.Profile.Secondary, extractorInstructions, text)
	if e != nil {
		completion.State = "unknown"
		completion.ErrorCode = "recording_failed"
		return ErrUnavailable
	}
	if outcome != "success" {
		completion.ErrorCode = outcome
		if ambiguous(outcome) {
			completion.State = "unknown"
		}
		return nil
	}
	completion.State = "succeeded"
	completion.Events = second.Extraction.Events
	return nil
}

func decodeRemoteSourceReference(raw []byte, provider string) (string, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return "", false
	}
	values := map[string]string{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || name != "Provider" && name != "RemoteID" {
			return "", false
		}
		if _, seen := values[name]; seen {
			return "", false
		}
		value, err := d.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return "", false
		}
		values[name] = text
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') || len(values) != 2 || d.Decode(new(any)) != io.EOF || values["Provider"] != provider || !mailsync.ValidRemoteID(values["RemoteID"]) {
		return "", false
	}
	return values["RemoteID"], true
}

// Authenticated references still require the exact format written by mailsync.
// Token decoding rejects duplicate/unknown/case-folded keys and trailing JSON.
func decodeSourceReference(raw []byte) (validity, uid uint32, valid bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return 0, 0, false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] || (name != "UIDValidity" && name != "UID") {
			return 0, 0, false
		}
		seen[name] = true
		var value uint32
		// Decode into a non-null integer token: json.Unmarshal(null,&uint32)
		// alone would otherwise silently leave the zero value unchanged.
		if err = d.Decode(&value); err != nil || value == 0 {
			return 0, 0, false
		}
		if name == "UIDValidity" {
			validity = value
		} else {
			uid = value
		}
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') || len(seen) != 2 {
		return 0, 0, false
	}
	if d.Decode(new(any)) != io.EOF {
		return 0, 0, false
	}
	return validity, uid, true
}

func SourceErrorCode(err error) string {
	var a mailaccounts.AdapterError
	var p *mailaccounts.AdapterError
	code := ""
	if errors.As(err, &a) {
		code = a.Code
	} else if errors.As(err, &p) && p != nil {
		code = p.Code
	}
	switch code {
	case "source_missing", "input_too_large", "unsupported_content", "authentication_failed", "credentials_unavailable", "uid_validity_changed", "tls_failed", "connection_failed", "timeout", "invalid_response", "cancelled":
		return code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "connection_failed"
}
func ambiguous(outcome string) bool {
	return outcome == "timeout" || outcome == "transport_error" || outcome == "request_cancelled" || outcome == "response_read" || outcome == "execution_panic"
}
func classify(err error) (string, int) {
	if err == nil {
		return "success", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", 0
	}
	if errors.Is(err, context.Canceled) {
		return "request_cancelled", 0
	}
	var e *ai.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_request", "transport_error", "request_cancelled", "response_read", "response_too_large", "invalid_json", "provider_http", "provider_error", "model_mismatch", "invalid_output", "refusal", "incomplete", "generation_mismatch":
			return e.Code, e.HTTPStatus
		}
	}
	return "provider_unavailable", 0
}
func safeDo(ctx context.Context, client InferenceClient, in ai.Request) (result ai.Response, err error) {
	defer func() {
		if recover() != nil {
			result = ai.Response{}
			err = &ai.Error{Code: "transport_error"}
		}
	}()
	return client.Do(ctx, in)
}
func (s *Service) call(ctx context.Context, l Lease, w Work, stage string, model airuntime.ModelRef, instructions string, text []byte) (ai.Response, string, error) {
	empty := ai.Response{}
	if ctx.Err() != nil {
		return empty, "request_cancelled", nil
	}
	if s.resolve == nil || s.client == nil {
		return empty, "credentials_unavailable", nil
	}
	key, e := s.resolve(ctx, model.CredentialID)
	defer clear(key)
	if e != nil || len(key) == 0 {
		return empty, "credentials_unavailable", nil
	}
	call, e := s.repo.BeginCall(ctx, l, stage)
	if e != nil {
		if errors.Is(e, ErrNotFound) || errors.Is(e, ErrConflict) {
			return empty, "configuration_unavailable", nil
		}
		return empty, "recording_failed", e
	}
	callCtx, stop := context.WithTimeout(ctx, time.Duration(w.Profile.TimeoutSeconds)*time.Second)
	response, e := safeDo(callCtx, s.client, ai.Request{Provider: model.Provider, ModelID: model.ModelID, ProviderSlug: w.Profile.OpenRouterProvider, WorkspaceID: w.Profile.AnthropicWorkspaceID, Input: ai.RequestInput{Text: string(text), Instructions: instructions}, Key: key, MaxOutputTokens: w.Profile.MaxOutputTokens, Price: model.Price})
	outcome, status := classify(e)
	if callCtx.Err() != nil {
		outcome, _ = classify(callCtx.Err())
	}
	stop()
	if outcome == "success" {
		if stage == "classification" && (response.Classification == nil || (response.Classification.Label != "subscription_related" && response.Classification.Label != "not_related" && response.Classification.Label != "uncertain")) {
			outcome = "invalid_output"
		}
		if stage == "extraction" {
			if response.Extraction == nil {
				outcome = "invalid_output"
			} else {
				raw, e := json.Marshal(response.Extraction)
				if e != nil {
					outcome = "invalid_output"
				} else if _, e = ai.ValidateExtraction(raw); e != nil {
					outcome = "invalid_output"
				}
				clear(raw)
			}
		}
	}
	if response.Cost.Status == "" {
		response.Cost = ai.Cost{Status: "unknown", Reason: "missing_usage"}
	}
	save, stopSave := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopSave()
	_, e = s.repo.FinishCall(save, call.ID, airuntime.CallUpdate{Outcome: outcome, HTTPStatus: status, GenerationID: response.GenerationID, ActualModelID: response.ActualModelID, ActualProvider: response.ActualProvider, Usage: response.Usage, Cost: response.Cost})
	if e != nil {
		return response, "recording_failed", e
	}
	return response, outcome, nil
}
