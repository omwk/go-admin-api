package file

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

// Link distinguishes permanent public addresses from five-minute private URLs.
type Link struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

func (s *Service) link(ctx context.Context, row model.FileObject, key string, preview bool) (Link, error) {
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return Link{}, err
	}
	if bound.accessMode == "public" {
		object, ok := bound.object.(interface{ PublicURL(string) (string, error) })
		if !ok {
			return Link{}, errors.New("public file links unavailable")
		}
		value, err := object.PublicURL(key)
		return Link{URL: value}, err
	}
	if preview {
		if object, ok := bound.object.(interface {
			PresignPreview(context.Context, string) (string, error)
		}); ok {
			value, err := object.PresignPreview(ctx, key)
			return Link{URL: value, ExpiresIn: 300}, err
		}
	}
	signer, ok := bound.object.(interface {
		PresignGet(context.Context, string) (string, error)
	})
	if !ok {
		return Link{}, errors.New("file signing unavailable")
	}
	value, err := signer.PresignGet(ctx, key)
	return Link{URL: value, ExpiresIn: 300}, err
}

func (s *Service) project(ctx context.Context, row model.FileObject) (View, error) {
	view := projectFile(row)
	if view.Category != "image" || row.ThumbKey == "" {
		return view, nil
	}
	link, err := s.link(ctx, row, row.ThumbKey, true)
	if err != nil {
		return View{}, err
	}
	view.ThumbURL = &link.URL
	return view, nil
}
