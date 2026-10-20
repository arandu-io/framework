package bootstrap

import (
	"fmt"
	"time"
)

// QueueConnection is where queued work is stored.
type QueueConnection string

// The supported connections. Both implement the same queue contract, so the
// worker, the handlers and every queue command are the same whichever one this
// value names.
const (
	// QueueDatabase stores jobs in a table of the application's own database,
	// which is what makes a job commitable by the same transaction as the row it
	// is about.
	QueueDatabase QueueConnection = "database"
	// QueueRedis stores them over RESP, for volume beyond a table. Its client is
	// a connector the binary links by a blank import of
	// github.com/arandu-io/hesape/queue/connectors/redis; naming it without the
	// import stops the boot saying which one to add.
	QueueRedis QueueConnection = "redis"
)

// Queue is where queued work is stored, and how a worker runs it.
//
// None of the four worker settings is given a default here. An unset variable
// leaves a zero, and the queue component reads a zero on each of them as the
// value it keeps by default, so leaving one out is how that component is asked
// for its own answer. A value that is present and cannot be used stops the boot
// naming itself.
type Queue struct {
	// Connection is QUEUE_CONNECTION: database, the default, or redis.
	Connection QueueConnection

	// Default is the queue a job goes to when it names none, and the queue
	// queue:work drains when it is not told otherwise: QUEUE_DEFAULT. One name
	// for both, so a deployment cannot dispatch into a queue no worker reads.
	Default string

	// Workers is how many jobs one queue:work process runs at once:
	// QUEUE_WORKERS. The --workers flag overrides it for one run.
	Workers int

	// RetryAfter is how long a lease lasts: QUEUE_RETRY_AFTER, in seconds. A
	// job whose worker died is picked up again after it, so it has to exceed the
	// longest job or the same work runs twice.
	RetryAfter time.Duration

	// MaxAttempts is how many times a failing job is tried before it is parked:
	// QUEUE_MAX_ATTEMPTS.
	MaxAttempts int
}

func loadQueueSettings() (Queue, error) {
	connection := QueueConnection(env("QUEUE_CONNECTION", string(QueueDatabase)))
	switch connection {
	case QueueDatabase:
	case QueueRedis:
		raw := env("REDIS_URL", "")
		if raw == "" {
			return Queue{}, fmt.Errorf("QUEUE_CONNECTION %q requires REDIS_URL", connection)
		}
		if _, err := parseRedisURL(raw); err != nil {
			return Queue{}, err
		}
	default:
		return Queue{}, fmt.Errorf("QUEUE_CONNECTION has unsupported value %q; expected database or redis", connection)
	}
	workers, err := envCount("QUEUE_WORKERS")
	if err != nil {
		return Queue{}, err
	}
	// Zero is the component's own lease: envSeconds refuses a value that is
	// present and not positive, so the only way to reach it is to leave the
	// variable out.
	retryAfter, err := envSeconds("QUEUE_RETRY_AFTER", 0)
	if err != nil {
		return Queue{}, err
	}
	maxAttempts, err := envCount("QUEUE_MAX_ATTEMPTS")
	if err != nil {
		return Queue{}, err
	}
	return Queue{
		Connection:  connection,
		Default:     env("QUEUE_DEFAULT", ""),
		Workers:     workers,
		RetryAfter:  retryAfter,
		MaxAttempts: maxAttempts,
	}, nil
}
