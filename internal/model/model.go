// Package model contains the relay's domain types.
package model

import "time"

// DeliveryStatus is the durable state of a webhook delivery.
type DeliveryStatus string

const (
	StatusPending    DeliveryStatus = "pending"
	StatusProcessing DeliveryStatus = "processing"
	StatusRetrying   DeliveryStatus = "retrying"
	StatusDelivered  DeliveryStatus = "delivered"
	StatusDeadLetter DeliveryStatus = "dead_letter"
)

// Channel defines how one named webhook channel is delivered.
type Channel struct {
	Name        string        `json:"name"`
	TargetURL   string        `json:"targetURL"`
	Secret      string        `json:"-"`
	Timeout     time.Duration `json:"timeout"`
	MaxAttempts int           `json:"maxAttempts"`
}

// Delivery is a persisted inbound webhook and its current delivery state.
type Delivery struct {
	ID             string         `json:"id"`
	Channel        string         `json:"channel"`
	ReceivedAt     time.Time      `json:"receivedAt"`
	Status         DeliveryStatus `json:"status"`
	Attempts       int            `json:"attempts"`
	NextAttemptAt  *time.Time     `json:"nextAttemptAt,omitempty"`
	ResponseStatus *int           `json:"responseStatus,omitempty"`
	LastError      string         `json:"lastError,omitempty"`
	PayloadHash    string         `json:"payloadHash"`
	Payload        []byte         `json:"-"`
	ContentType    string         `json:"-"`
	IdempotencyKey string         `json:"-"`
}
