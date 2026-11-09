package mock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
)

// RefreshProtocol identifies the local fixture protocol, never a remote platform grant.
func (*Platform) RefreshProtocol() port.Protocol {
	return port.Protocol{Verified: true, ExclusiveGrant: true}
}

// Refresh rotates an opaque fixture bundle without retaining or logging input values.
func (*Platform) Refresh(ctx context.Context, request port.Request) (port.Result, error) {
	if err := ctx.Err(); err != nil {
		return port.Result{}, err
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(request.Values, &values) != nil || len(values) == 0 {
		return port.Result{}, errors.New("invalid mock credential bundle")
	}
	defer func() {
		for _, v := range values {
			clear(v)
		}
	}()
	for key := range values {
		var material [32]byte
		if _, err := rand.Read(material[:]); err != nil {
			return port.Result{}, errors.New("mock refresh entropy unavailable")
		}
		encoded, err := json.Marshal(hex.EncodeToString(material[:]))
		clear(material[:])
		if err != nil {
			return port.Result{}, errors.New("mock refresh encoding failed")
		}
		clear(values[key])
		values[key] = encoded
	}
	result, err := json.Marshal(values)
	if err != nil {
		return port.Result{}, errors.New("mock refresh encoding failed")
	}
	now := time.Now().UTC()
	return port.Result{Values: result, ExpiresAt: now.Add(4 * time.Hour), RefreshAt: now.Add(3 * time.Hour)}, nil
}
