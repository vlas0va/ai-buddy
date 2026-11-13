package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer отправляет одно сообщение в Kafka и возвращается после подтверждения. В тестах используется подделка.
type Producer interface {
	Produce(ctx context.Context, topic, key string, value []byte) error
}

// DefaultInterval — как часто Publisher.Run выгружает outbox.
const DefaultInterval = 2 * time.Second

const drainBatch = 200

// Publisher переносит строки core.outbox в Kafka в порядке id и проставляет published_at.
type Publisher struct {
	pool     *pgxpool.Pool
	prod     Producer
	log      *slog.Logger
	Interval time.Duration
}

// NewPublisher возвращает Publisher, выгружающий раз в DefaultInterval.
func NewPublisher(pool *pgxpool.Pool, prod Producer, log *slog.Logger) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{pool: pool, prod: prod, log: log, Interval: DefaultInterval}
}

// Run выгружает outbox раз в Interval, пока ctx не отменён. Ошибки логируются и повторяются на следующем тике.
func (p *Publisher) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := p.DrainOnce(ctx); err != nil {
				p.log.Warn("outbox drain failed", "published", n, "err", err)
			}
		}
	}
}

// DrainOnce публикует до одной пачки неопубликованных строк в порядке id. Останавливается на первой ошибке продюсера
// (порядок сохраняется), предварительно отметив уже отправленные строки, и возвращает число опубликованных.
func (p *Publisher) DrainOnce(ctx context.Context) (int, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin drain: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id, topic, COALESCE(payload->>'user_id', payload->>'event_id', ''), payload::text
		FROM core.outbox WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, drainBatch)
	if err != nil {
		return 0, fmt.Errorf("select outbox: %w", err)
	}
	type msg struct {
		id         int64
		topic, key string
		payload    []byte
	}
	var msgs []msg
	for rows.Next() {
		var m msg
		var payload string
		if err := rows.Scan(&m.id, &m.topic, &m.key, &payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan outbox: %w", err)
		}
		m.payload = []byte(payload)
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read outbox: %w", err)
	}
	var sent []int64
	var produceErr error
	for _, m := range msgs {
		if produceErr = p.prod.Produce(ctx, m.topic, m.key, m.payload); produceErr != nil {
			break
		}
		sent = append(sent, m.id)
	}
	if len(sent) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE core.outbox SET published_at = now() WHERE id = ANY($1)`, sent); err != nil {
			return 0, fmt.Errorf("stamp outbox: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit drain: %w", err)
		}
	}
	if produceErr != nil {
		return len(sent), fmt.Errorf("produce: %w", produceErr)
	}
	return len(sent), nil
}

// KafkaProducer — реализация Producer на franz-go.
type KafkaProducer struct {
	cl      *kgo.Client
	timeout time.Duration
}

// NewKafkaProducer лениво подключается к seed-брокерам (host:port).
func NewKafkaProducer(brokers []string) (*KafkaProducer, error) {
	if len(brokers) == 0 {
		return nil, errors.New("analytics: no kafka brokers")
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	return &KafkaProducer{cl: cl, timeout: 10 * time.Second}, nil
}

// Produce отправляет сообщение синхронно.
func (k *KafkaProducer) Produce(ctx context.Context, topic, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()
	res := k.cl.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
	return res.FirstErr()
}

// Close освобождает клиента.
func (k *KafkaProducer) Close() { k.cl.Close() }
