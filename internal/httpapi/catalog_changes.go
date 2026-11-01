package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/modelchanges"
	"net/url"
	"strconv"
)

type catalogChangesService interface {
	CatalogChanges(context.Context, modelchanges.Filter) (modelchanges.Page, error)
}

func catalogChangesFilter(raw string) (modelchanges.Filter, error) {
	f := modelchanges.Filter{}
	if len(raw) > 2048 {
		return f, modelcatalog.ErrInvalid
	}
	p, e := url.ParseQuery(raw)
	if e != nil {
		return f, modelcatalog.ErrInvalid
	}
	for key, values := range p {
		if len(values) != 1 {
			return f, modelcatalog.ErrInvalid
		}
		switch key {
		case "provider":
			f.Provider = values[0]
		case "model_id":
			f.ModelID = values[0]
		case "kind":
			f.Kind = values[0]
		case "limit":
			f.Limit, e = strconv.Atoi(values[0])
			if e != nil || f.Limit < 1 {
				return f, modelcatalog.ErrInvalid
			}
		case "offset":
			f.Offset, e = strconv.Atoi(values[0])
			if e != nil {
				return f, modelcatalog.ErrInvalid
			}
		default:
			return f, modelcatalog.ErrInvalid
		}
	}
	return modelchanges.Validate(f)
}
