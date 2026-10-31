package db

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Fork creates independent count/list branches from one filtered base relation.
func (q *ScopedQuery) Fork() *ScopedQuery {
	if q.err != nil {
		return &ScopedQuery{cancel: func() {}, err: q.err}
	}
	return &ScopedQuery{handle: q.handle.Session(&gorm.Session{}), cancel: func() {}, err: q.err}
}

// Distinct deduplicates root identifiers when an adapter joins one-to-many relations.
func (q *ScopedQuery) Distinct(columns ...any) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Distinct(columns...)
	}
	return q
}

// Lock requests row locking on an already scoped relation.
func (q *ScopedQuery) Lock() *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return q
}

// Take reads one explicitly projected row without adding an implicit ordering.
func (q *ScopedQuery) Take(dest any) error {
	if q.err != nil {
		return q.err
	}
	if len(q.handle.Statement.Selects) == 0 {
		return errors.New("query selected columns are required")
	}
	q.handle = q.handle.Take(dest)
	return q.handle.Error
}

// RowsAffected exposes mutation cardinality without exposing the database handle.
func (q *ScopedQuery) RowsAffected() int64 { return q.handle.RowsAffected }

// Group groups an adapter's fixed root-entity projection.
func (q *ScopedQuery) Group(columns string) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Group(columns)
	}
	return q
}
