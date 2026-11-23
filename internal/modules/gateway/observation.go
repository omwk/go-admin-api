package gateway

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// ObservationService owns range limits and management-only manual invocation.
type ObservationService struct {
	store      ObservationStore
	now        func() time.Time
	platformID int64
	dispatch   TrialDispatcher
}

// NewObservationService requires an explicit platform tenant and real dispatcher.
func NewObservationService(store ObservationStore, now func() time.Time, platformID int64, dispatch TrialDispatcher) (*ObservationService, error) {
	if store == nil || now == nil || platformID <= 0 || dispatch == nil {
		return nil, errors.New("gateway observation dependencies required")
	}
	return &ObservationService{store: store, now: now, platformID: platformID, dispatch: dispatch}, nil
}

func (s *ObservationService) filter(values url.Values, summary bool) (CallFilter, error) {
	f := CallFilter{To: s.now().UTC(), Page: 1, PageSize: 20, Status: values.Get("status"), TraceID: values.Get("trace_id"), GroupBy: values.Get("group_by")}
	for _, items := range values {
		if len(items) != 1 {
			return f, Invalid()
		}
	}
	if raw := values.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return f, Invalid()
		}
		f.To = parsed.UTC()
	}
	f.From = f.To.Add(-24 * time.Hour)
	if raw := values.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return f, Invalid()
		}
		f.From = parsed.UTC()
	}
	if !f.From.Before(f.To) || f.To.Sub(f.From) > 30*24*time.Hour || f.Status != "" && f.Status != "success" && f.Status != "failed" || len(f.TraceID) > 64 {
		return f, Invalid()
	}
	for name, target := range map[string]*int64{"client_id": &f.ClientID, "route_id": &f.RouteID} {
		if raw := values.Get(name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 {
				return f, Invalid()
			}
			*target = n
		}
	}
	for name, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if raw := values.Get(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 || n > 1000000 {
				return f, Invalid()
			}
			*target = n
		}
	}
	if f.PageSize > 100 || summary && f.GroupBy != "client" && f.GroupBy != "route" && f.GroupBy != "day" {
		return f, Invalid()
	}
	return f, nil
}

func (s *ObservationService) read(ctx context.Context, scope ObservationScope, values url.Values, summary bool) (any, error) {
	f, err := s.filter(values, summary)
	if err != nil {
		return nil, err
	}
	if f.ClientID > 0 {
		if _, err := s.store.ObservationClient(ctx, scope, f.ClientID); err != nil {
			return nil, err
		}
	}
	if summary {
		return s.store.SummarizeCalls(ctx, scope, f)
	}
	return s.store.ListCalls(ctx, scope, f)
}
