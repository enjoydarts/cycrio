// Package modelchanges exposes the observed catalog change journal.
package modelchanges

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"slices"
	"time"
)

type Filter struct {
	Provider, ModelID, Kind string
	Limit, Offset           int
}
type Change struct {
	ID              string          `json:"id"`
	ModelID         string          `json:"model_id"`
	Provider        string          `json:"provider"`
	ModelName       string          `json:"model_name"`
	UpstreamModelID string          `json:"upstream_model_id"`
	Kind            string          `json:"kind"`
	OccurredAt      time.Time       `json:"occurred_at"`
	SourceURL       string          `json:"source_url"`
	Before          json.RawMessage `json:"before"`
	After           json.RawMessage `json:"after"`
}
type Page struct {
	Changes []Change `json:"changes"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}
type Repository interface {
	List(context.Context, Filter) (Page, error)
}
type Service struct{ repo Repository }

func NewService(r Repository) *Service { return &Service{r} }
func Validate(f Filter) (Filter, error) {
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 1000000 || f.Provider != "" && !modelcatalog.ValidProvider(f.Provider) || !slices.Contains([]string{"", "added", "updated", "retired", "restored", "price_changed", "deleted"}, f.Kind) {
		return f, modelcatalog.ErrInvalid
	}
	if f.ModelID != "" {
		if _, e := uuid.Parse(f.ModelID); e != nil {
			return f, modelcatalog.ErrInvalid
		}
	}
	return f, nil
}
func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	f, e := Validate(f)
	if e != nil {
		return Page{}, e
	}
	return s.repo.List(ctx, f)
}
