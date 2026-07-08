package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ErrClosed is returned by Poll once the underlying client has been closed,
// signalling the consume loop to stop.
var ErrClosed = errors.New("kafka: consumer closed")

// Consumer reads records from a single topic as part of a consumer group with
// manual offset commits, enabling at-least-once processing: offsets are only
// committed after the caller has durably handled the polled batch.
//
// A Consumer is not safe for concurrent use: it retains the last polled batch so
// Commit can advance exactly those offsets, so callers must drive Poll and
// Commit sequentially from a single goroutine.
type Consumer struct {
	client  *kgo.Client
	maxPoll int
	// last holds the records returned by the most recent Poll so a subsequent
	// Commit advances exactly the offsets the caller has processed.
	last []*kgo.Record
}

// NewConsumer joins group on the given brokers and consumes topic. Automatic
// commits are disabled so the caller controls when offsets advance. maxPoll
// bounds how many records a single Poll returns, capping the batch size.
func NewConsumer(brokers []string, group, topic string, maxPoll int) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka: create consumer: %w", err)
	}
	return &Consumer{client: client, maxPoll: maxPoll}, nil
}

// Poll blocks until at least one record is available or ctx is done, returning
// up to maxPoll record values. It returns ErrClosed once the client is closed.
func (c *Consumer) Poll(ctx context.Context) ([][]byte, error) {
	fetches := c.client.PollRecords(ctx, c.maxPoll)
	if fetches.IsClientClosed() {
		return nil, ErrClosed
	}
	if err := fetches.Err(); err != nil {
		return nil, fmt.Errorf("kafka: fetch: %w", err)
	}

	records := make([]*kgo.Record, 0, fetches.NumRecords())
	values := make([][]byte, 0, fetches.NumRecords())
	fetches.EachRecord(func(r *kgo.Record) {
		records = append(records, r)
		values = append(values, r.Value)
	})
	c.last = records
	return values, nil
}

// Commit synchronously commits the offsets of the records returned by the most
// recent Poll. It is a no-op when the last poll yielded no records.
func (c *Consumer) Commit(ctx context.Context) error {
	if len(c.last) == 0 {
		return nil
	}
	if err := c.client.CommitRecords(ctx, c.last...); err != nil {
		return fmt.Errorf("kafka: commit: %w", err)
	}
	return nil
}

// Ping verifies broker connectivity.
func (c *Consumer) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: ping: %w", err)
	}
	return nil
}

// Close leaves the consumer group and releases the underlying client.
func (c *Consumer) Close() {
	c.client.Close()
}
