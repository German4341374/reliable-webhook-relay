package storage

import (
	"context"
	"testing"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/model"
)

func TestCreateDeliveryIsIdempotentPerChannel(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), t.TempDir()+"/relay.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	first := model.Delivery{
		ID:             "first",
		Channel:        "orders",
		ReceivedAt:     now,
		Status:         model.StatusPending,
		NextAttemptAt:  &now,
		PayloadHash:    "hash-one",
		Payload:        []byte("first"),
		ContentType:    "application/json",
		IdempotencyKey: "event-42",
	}
	persisted, created, err := store.CreateDelivery(context.Background(), first)
	if err != nil || !created {
		t.Fatalf("first insert failed: created=%v err=%v", created, err)
	}
	if persisted.ID != first.ID {
		t.Fatalf("first ID = %q, want %q", persisted.ID, first.ID)
	}

	second := first
	second.ID = "second"
	second.Payload = []byte("different")
	second.PayloadHash = "hash-two"
	persisted, created, err = store.CreateDelivery(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate idempotency key created another delivery")
	}
	if persisted.ID != first.ID || string(persisted.Payload) != "first" {
		t.Fatal("duplicate request did not return the original delivery")
	}
}

func TestIdempotencyKeyCanRepeatAcrossChannels(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), t.TempDir()+"/relay.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	for _, channel := range []string{"orders", "billing"} {
		delivery := model.Delivery{
			ID:             channel,
			Channel:        channel,
			ReceivedAt:     now,
			Status:         model.StatusPending,
			NextAttemptAt:  &now,
			PayloadHash:    "hash",
			Payload:        []byte("body"),
			ContentType:    "text/plain",
			IdempotencyKey: "same-key",
		}
		if _, created, err := store.CreateDelivery(context.Background(), delivery); err != nil || !created {
			t.Fatalf("channel %q insert: created=%v err=%v", channel, created, err)
		}
	}
}
