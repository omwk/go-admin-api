// Package port defines storage contracts consumed by catalog exports.
package port

import (
	"context"
	"io"
	"time"
)

// Object carries the streamed body and size reported by private storage.
type Object struct {
	Body io.ReadCloser
	Size int64
}

// ObjectStore supports private streaming uploads and bounded signed downloads.
type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64) (int64, error)
	Get(context.Context, string) (Object, error)
	Delete(context.Context, string) error
	PresignGet(context.Context, string, time.Duration) (string, error)
}

// ObjectInfo is the bounded metadata used by orphan collection.
type ObjectInfo struct {
	Key        string
	ModifiedAt time.Time
}

// ObjectScanner streams a prefix inventory without buffering all keys.
type ObjectScanner interface {
	Scan(context.Context, string, func(ObjectInfo) error) error
}
