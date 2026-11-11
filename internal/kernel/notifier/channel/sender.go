// Package channel provides configurable notification transports and durable delivery.
package channel

import "context"

// Message exists only in memory; recipients must never be persisted with it.
type Message struct {
	ID        int64
	Recipient string
	Subject   string
	Body      string
	Format    string
	Variables map[string]string
}

// Config contains provider-specific settings and must be encrypted at rest.
type Config map[string]string

// Sender delivers through one configured provider.
type Sender interface {
	Channel() string
	Kind() string
	Send(context.Context, Message) error
	Test(context.Context) error
}
