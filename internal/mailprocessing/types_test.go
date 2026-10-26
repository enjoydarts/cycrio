package mailprocessing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airuntime"
)

func TestPublicModelsExposeOnlyApprovedOpenRouterRoutingProvider(t *testing.T) {
	for _, provider := range []string{"openrouter", "openai", "anthropic"} {
		p := airuntime.Profile{Primary: airuntime.ModelRef{Provider: "liquid"}, Secondary: airuntime.ModelRef{Provider: provider}, OpenRouterProvider: "approved/provider"}
		public := Public(p, Policy{})
		want := ""
		if provider == "openrouter" {
			want = "approved/provider"
		}
		if public.Primary.RoutingProvider != "" || public.Secondary.RoutingProvider != want {
			t.Fatal("routing provider projection", public)
		}
		data, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"routing_provider":`) {
			t.Fatal("missing public routing metadata")
		}
	}
}
