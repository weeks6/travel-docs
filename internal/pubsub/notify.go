package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"
)

// Notify publishes ev as JSON on topic. Live events are best effort (clients
// can always reload the current state through the API), so a failure is
// logged rather than returned.
func Notify(ctx context.Context, b Broker, log *slog.Logger, topic string, ev any) {
	data, err := json.Marshal(ev)
	if err == nil {
		err = b.Publish(ctx, topic, data)
	}
	if err != nil && ctx.Err() == nil {
		log.Warn("publish event", "topic", topic, "error", err)
	}
}
