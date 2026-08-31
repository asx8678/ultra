package domain

import "context"

const AuditTopic = "ORDER_AUDIT_TOPIC"

type Order struct {
	ID string
}

type OrderStore interface {
	LoadOrder(context.Context, string) (Order, error)
}

func LoadOrder(ctx context.Context, store OrderStore, id string) (Order, error) {
	return store.LoadOrder(ctx, id)
}
