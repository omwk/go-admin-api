package catalog

import "context"

// ExportListQuery is the validated pagination for export history.
type ExportListQuery struct {
	Page     int
	PageSize int
}

// ExportPage exposes safe job metadata without signed URLs or storage pointers.
type ExportPage struct {
	List     []ExportJob `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// List returns only the current tenant and requester's product export jobs.
func (s *ExportService) List(ctx context.Context, tenant, user int64, query ExportListQuery) (ExportPage, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ExportPage{}, err
	}
	return s.store.ListExports(ctx, tenant, user, query)
}
