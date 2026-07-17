package gosmee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisStreamKeyPrefix    = "gosmee:stream:"
	redisStreamPayloadField = "payload"
)

type relayEvent struct {
	ID         string
	Data       []byte
	DeliveryID string
	EventType  string
}

func relayEventMetadata(data []byte) (deliveryID, eventType string) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", ""
	}
	for key, value := range payload {
		value, ok := value.(string)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "x-github-delivery", "x-gitea-delivery", "x-forgejo-delivery", "x-gitlab-delivery", "x-event-id":
			if deliveryID == "" {
				deliveryID = value
			}
		case "x-github-event", "x-gitlab-event", "x-gitea-event", "x-forgejo-event", "x-event-key":
			if eventType == "" {
				eventType = value
			}
		}
	}
	return deliveryID, eventType
}

type payloadRelay interface {
	Publish(ctx context.Context, channel string, data []byte) (string, error)
}

type localPayloadRelay struct {
	eventBroker *EventBroker
}

func newLocalPayloadRelay(eventBroker *EventBroker) *localPayloadRelay {
	return &localPayloadRelay{
		eventBroker: eventBroker,
	}
}

func (r *localPayloadRelay) Publish(_ context.Context, channel string, data []byte) (string, error) {
	r.eventBroker.PublishEvent(channel, relayEvent{Data: data})
	return "", nil
}

type redisStreamClient interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	Close() error
}

type redisPayloadRelay struct {
	client    redisStreamClient
	keyPrefix string
	maxLen    int64
}

func newRedisPayloadRelay(ctx context.Context, redisURL string, maxLen int64) (*redisPayloadRelay, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	relay := &redisPayloadRelay{
		client:    client,
		keyPrefix: redisStreamKeyPrefix,
		maxLen:    maxLen,
	}

	return relay, nil
}

func newRedisPayloadRelayWithClient(client redisStreamClient, maxLen int64) *redisPayloadRelay {
	return &redisPayloadRelay{
		client:    client,
		keyPrefix: redisStreamKeyPrefix,
		maxLen:    maxLen,
	}
}

func (r *redisPayloadRelay) Publish(ctx context.Context, channel string, data []byte) (string, error) {
	args := &redis.XAddArgs{
		Stream: r.streamKey(channel),
		Values: map[string]any{
			redisStreamPayloadField: string(data),
		},
	}
	if r.maxLen > 0 {
		args.MaxLen = r.maxLen
		args.Approx = true
	}

	id, err := r.client.XAdd(ctx, args).Result()
	if err != nil {
		return "", fmt.Errorf("write to redis stream: %w", err)
	}
	return id, nil
}

func (r *redisPayloadRelay) Read(ctx context.Context, channel, afterID string, block time.Duration, count int64) ([]relayEvent, error) {
	streams, err := r.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{r.streamKey(channel), afterID},
		Block:   block,
		Count:   count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read redis stream: %w", err)
	}

	events := make([]relayEvent, 0)
	for _, stream := range streams {
		for _, message := range stream.Messages {
			payload, ok := message.Values[redisStreamPayloadField]
			if !ok {
				return nil, fmt.Errorf("redis stream entry %s missing %q field", message.ID, redisStreamPayloadField)
			}
			data, err := redisPayloadBytes(payload)
			if err != nil {
				return nil, fmt.Errorf("redis stream entry %s payload: %w", message.ID, err)
			}
			deliveryID, eventType := relayEventMetadata(data)
			events = append(events, relayEvent{ID: message.ID, Data: data, DeliveryID: deliveryID, EventType: eventType})
		}
	}

	return events, nil
}

func redisPayloadBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return nil, fmt.Errorf("unexpected type %T", value)
	}
}

func (r *redisPayloadRelay) OldestID(ctx context.Context, channel string) (string, bool, error) {
	messages, err := r.client.XRangeN(ctx, r.streamKey(channel), "-", "+", 1).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read oldest redis stream id: %w", err)
	}
	if len(messages) == 0 {
		return "", false, nil
	}
	return messages[0].ID, true, nil
}

func (r *redisPayloadRelay) NewestID(ctx context.Context, channel string) (string, bool, error) {
	messages, err := r.client.XRevRangeN(ctx, r.streamKey(channel), "+", "-", 1).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read newest redis stream id: %w", err)
	}
	if len(messages) == 0 {
		return "", false, nil
	}
	return messages[0].ID, true, nil
}

func (r *redisPayloadRelay) Close() error {
	return r.client.Close()
}

func (r *redisPayloadRelay) streamKey(channel string) string {
	return r.keyPrefix + channel
}
