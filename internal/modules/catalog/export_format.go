package catalog

import (
	"errors"
	"strings"
)

// ExportFormat is a server-owned CSV contract for one kind of materialized rows.
type ExportFormat struct {
	Header []string
	Record func(ExportRow) ([]string, error)
}

// UseFormat registers a contract during bootstrap, before requests or worker execution.
func (s *ExportService) UseFormat(kind string, format ExportFormat) error {
	if s == nil || kind == "" || strings.ContainsAny(kind, "/\\") || len(format.Header) == 0 || format.Record == nil {
		return errors.New("invalid export format")
	}
	if _, exists := s.formats[kind]; exists {
		return errors.New("export format already registered")
	}
	format.Header = append([]string(nil), format.Header...)
	s.formats[kind] = format
	return nil
}

func (s *ExportService) exportFormat(kind string) (ExportFormat, error) {
	format, ok := s.formats[kind]
	if !ok {
		return ExportFormat{}, errors.New("export format unavailable")
	}
	return format, nil
}
