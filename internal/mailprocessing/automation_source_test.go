package mailprocessing

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"testing"
)

func TestAutomaticPipelineForwardsStrictSourceGenerationAndFixedPauseCodes(t *testing.T) {
	for _, code := range []string{"uid_validity_changed", "authentication_failed", "credentials_unavailable"} {
		r, keys, lease := pipelineFixture(t)
		r.w.Automatic = true
		reader := textReader(func(_ context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
			if !source.StrictGeneration {
				t.Error("automatic source lost generation guard")
			}
			return nil, mailaccounts.AdapterError{Code: code}
		})
		service := NewService(r, keys, reader, nil, nil)
		if e := service.Execute(context.Background(), lease); e != nil || r.completion.ErrorCode != code || r.completion.State != "failed" || r.calls != 0 {
			t.Fatal(code, r.completion, e)
		}
	}
}
