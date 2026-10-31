package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ScopedQuery exposes the query operations allowed to repositories without exposing GORM.
type ScopedQuery struct {
	handle *gorm.DB
	cancel context.CancelFunc
	err    error
}

type scopeKey struct{}

// Scope constrains a model query at the trusted composition boundary.
// Implementations must reject missing identity and apply tenant and data predicates.
type Scope func(context.Context, any, *gorm.DB) (*gorm.DB, error)

// WithScope attaches a trusted query policy; repositories cannot import this package.
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// Scoped binds a model and request context to a repository query facade.
func (d *DB) Scoped(ctx context.Context, model any) *ScopedQuery {
	handle, cancel := d.WithContext(ctx)
	return BindScoped(ctx, model, handle, cancel)
}

// BindScoped binds a trusted adapter's handle to the same restricted facade as DB.Scoped.
func BindScoped(ctx context.Context, model any, handle *gorm.DB, cancel context.CancelFunc) *ScopedQuery {
	query := &ScopedQuery{handle: handle, cancel: cancel}
	if model == nil {
		query.err = errors.New("query model is required")
		return query
	}
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	if !ok || scope == nil {
		query.err = errors.New("query scope is required")
		return query
	}
	query.handle, query.err = scope(ctx, model, handle.Model(model))
	if query.handle == nil && query.err == nil {
		query.err = errors.New("query scope returned no query")
	}
	return query
}

// Close releases the query deadline created by Scoped.
func (q *ScopedQuery) Close() { q.cancel() }

// Select adds selected columns to the query.
func (q *ScopedQuery) Select(query any, args ...any) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Select(query, args...)
	}
	return q
}

// Where adds a predicate to the query.
func (q *ScopedQuery) Where(query any, args ...any) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Where(query, args...)
	}
	return q
}

// Joins adds a join to the query.
func (q *ScopedQuery) Joins(query string, args ...any) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Joins(query, args...)
	}
	return q
}

// Order adds a sort expression to the query.
func (q *ScopedQuery) Order(value any) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Order(value)
	}
	return q
}

// Limit restricts the number of rows returned.
func (q *ScopedQuery) Limit(limit int) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Limit(limit)
	}
	return q
}

// Offset skips rows before returning results.
func (q *ScopedQuery) Offset(offset int) *ScopedQuery {
	if q.err == nil {
		q.handle = q.handle.Offset(offset)
	}
	return q
}

// Find loads all matching rows into dest.
func (q *ScopedQuery) Find(dest any) error {
	if q.err != nil {
		return q.err
	}
	if len(q.handle.Statement.Selects) == 0 {
		return errors.New("query selected columns are required")
	}
	q.handle = q.handle.Find(dest)
	return q.handle.Error
}

// First loads the first matching row into dest.
func (q *ScopedQuery) First(dest any) error {
	if q.err != nil {
		return q.err
	}
	if len(q.handle.Statement.Selects) == 0 {
		return errors.New("query selected columns are required")
	}
	q.handle = q.handle.First(dest)
	return q.handle.Error
}

// Count writes the number of matching rows into count.
func (q *ScopedQuery) Count(count *int64) error {
	if q.err != nil {
		return q.err
	}
	q.handle = q.handle.Count(count)
	return q.handle.Error
}

// Create inserts value through the scoped model.
func (q *ScopedQuery) Create(value any) error {
	if q.err != nil {
		return q.err
	}
	q.handle = q.handle.Create(value)
	return q.handle.Error
}

// Updates applies values to rows matched by the query.
func (q *ScopedQuery) Updates(values any) error {
	if q.err != nil {
		return q.err
	}
	q.handle = q.handle.Updates(values)
	return q.handle.Error
}

// Delete removes rows matched by the query.
func (q *ScopedQuery) Delete(value any) error {
	if q.err != nil {
		return q.err
	}
	q.handle = q.handle.Delete(value)
	return q.handle.Error
}
