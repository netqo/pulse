// Package kafka wraps the franz-go client with the small producer surface the
// Pulse services need.
package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes keyed records to a single Kafka topic.
type Producer struct {
	client *kgo.Client
	topic  string
}

// NewProducer connects to the given brokers and targets topic. Auto topic
// creation is enabled so the local Redpanda stack works from a clean start.
func NewProducer(brokers []string, topic string) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka: create client: %w", err)
	}
	return &Producer{client: client, topic: topic}, nil
}

// Produce synchronously publishes a keyed record. Keying by symbol preserves
// per-symbol ordering by routing all of a symbol's records to one partition.
func (p *Producer) Produce(ctx context.Context, key, value []byte) error {
	record := &kgo.Record{Topic: p.topic, Key: key, Value: value}
	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("kafka: produce: %w", err)
	}
	return nil
}

// Ping verifies broker connectivity.
func (p *Producer) Ping(ctx context.Context) error {
	if err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: ping: %w", err)
	}
	return nil
}

// Close flushes buffered records and releases the underlying client.
func (p *Producer) Close() {
	p.client.Close()
}
