// Package events is domain events with an outbox.
//
// There is no Publish(). The naive flow loses data in both directions: if the
// process dies between the write and the publish, the event never leaves; if the
// publish happens and the transaction rolls back, the rest of the system reacts
// to something that did not happen.
//
// So an event is stored in the same transaction as the write that produced it,
// and a relay publishes it afterwards. One way to do it (RULE 9), and the one
// that cannot lose an event.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// Event is something that happened, in the past tense.
//
// The name is the vocabulary of the domain rather than of the database:
// "invoice.paid", not "invoice.updated". A consumer that has to diff two rows to
// learn what happened is a consumer coupled to your schema.
type Event struct {
	// Name identifies the event: "customer.created", "invoice.paid".
	Name string
	// Aggregate and AggregateID say what it happened to.
	Aggregate   string
	AggregateID string
	// Payload is what the consumer needs, serialized as JSON. Keep it to facts
	// that were true when the event occurred: an event that says "look it up" is
	// an event that reads a row which has already changed.
	Payload any
	// OccurredAt defaults to the moment it is stored.
	OccurredAt time.Time
}

// Recorder is what an entity embeds to collect its own events.
//
// The entity records; the service stores. That split is what keeps the entity
// free of a database handle and keeps the event next to the rule that produced
// it.
type Recorder struct {
	pending []Event
}

// Record adds an event to be stored with the next write.
func (r *Recorder) Record(e Event) {
	r.pending = append(r.pending, e)
}

// PullEvents returns the recorded events and clears them.
//
// Clearing is the point: an entity stored twice must not emit the same event
// twice, and the caller that pulls is the one that is about to store.
func (r *Recorder) PullEvents() []Event {
	pending := r.pending
	r.pending = nil
	return pending
}

// Outbox stores events in the same transaction as the write.
type Outbox struct {
	db *data.DB
}

// NewOutbox returns an outbox over the application's database handle.
func NewOutbox(db *data.DB) *Outbox { return &Outbox{db: db} }

// ErrNoTransaction is returned when Store is called outside data.Transaction.
//
// It is an error rather than a fallback, and that is the whole guarantee: an
// event stored next to a row that then rolled back is worse than no event, and
// an event stored after the commit is one process crash away from being lost.
var ErrNoTransaction = fmt.Errorf("events: Store must run inside data.Transaction")

// Store writes the events, inside the caller's transaction.
//
// The Grant goes into the row: who authorized it, which action, which tenant.
// That is a full audit trail without a second table, and it is why Store takes a
// Grant it does not otherwise need.
func (o *Outbox) Store(ctx context.Context, g security.Grant, list []Event) error {
	if len(list) == 0 {
		return nil
	}
	if !data.InTransaction(ctx) {
		return ErrNoTransaction
	}

	tenant := data.Tenant(g)
	if tenant == "" {
		// RULE 14: the tenant comes from the Grant, and a Grant without one
		// cannot scope anything. A relay reading this row would not know who to
		// deliver it to.
		return fmt.Errorf("events: the Grant carries no tenant")
	}

	now := time.Now().UTC()
	for _, e := range list {
		if e.Name == "" {
			return fmt.Errorf("events: an event with no name cannot be routed")
		}
		payload, err := json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("events: serializing %s: %w", e.Name, err)
		}
		id, err := data.NewID()
		if err != nil {
			return err
		}
		occurred := e.OccurredAt
		if occurred.IsZero() {
			occurred = now
		}

		_, err = o.db.ExecContext(ctx, `
			INSERT INTO outbox (
				id, tenant_id, event, aggregate, aggregate_id, payload,
				authorized_by, action, occurred_at, attempts
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			id, tenant, e.Name, e.Aggregate, e.AggregateID, string(payload),
			g.Subject().ID, string(g.Action()), occurred,
		)
		if err != nil {
			return fmt.Errorf("events: storing %s: %w", e.Name, err)
		}
	}
	return nil
}

// Pending returns one tenant's unpublished events, oldest first.
func (o *Outbox) Pending(ctx context.Context, tenant string, limit int) ([]Stored, error) {
	return o.query(ctx, `
		WHERE published_at IS NULL AND failed_at IS NULL AND tenant_id = ?
		ORDER BY occurred_at
		LIMIT ?`, tenant, sane(limit))
}

// PendingAll returns unpublished events across every tenant, oldest first.
//
// It takes no Grant, and that is deliberate rather than an oversight of RULE 17.
// The authorization already happened, at write time, and it is recorded in the
// row -- authorized_by and action are right there. The relay decides nothing:
// it delivers what was already permitted. This is the same shape as the migrator
// reading its own table, and it is the only read in the framework that works
// this way.
//
// It is also why the relay is infrastructure and not a route. Nothing here is
// reachable from a request.
func (o *Outbox) PendingAll(ctx context.Context, limit int) ([]Stored, error) {
	return o.query(ctx, `
		WHERE published_at IS NULL AND failed_at IS NULL
		ORDER BY occurred_at
		LIMIT ?`, sane(limit))
}

// Parked returns the events that gave up, newest failure first.
//
// A dead letter queue nobody can list is a table that grows. `aru doctor`
// reports the count, because an event that never left is a business process
// that silently did not happen.
func (o *Outbox) Parked(ctx context.Context, limit int) ([]Stored, error) {
	return o.query(ctx, `
		WHERE failed_at IS NOT NULL
		ORDER BY failed_at DESC
		LIMIT ?`, sane(limit))
}

// query runs the standard projection with a caller-supplied tail.
func (o *Outbox) query(ctx context.Context, tail string, args ...any) ([]Stored, error) {
	rows, err := o.db.QueryContext(ctx, `
		SELECT id, tenant_id, event, aggregate, aggregate_id, payload,
		       authorized_by, action, occurred_at, attempts, last_error
		FROM outbox`+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("events: reading the outbox: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Stored
	for rows.Next() {
		var s Stored
		var lastError sql.NullString
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.Aggregate, &s.AggregateID,
			&s.Payload, &s.AuthorizedBy, &s.Action, &s.OccurredAt, &s.Attempts, &lastError); err != nil {
			return nil, fmt.Errorf("events: reading the outbox: %w", err)
		}
		s.LastError = lastError.String
		out = append(out, s)
	}
	return out, rows.Err()
}

func sane(limit int) int {
	if limit <= 0 {
		return 100
	}
	return limit
}

// Park stops retrying an event and records why.
//
// An event that failed ten times will not succeed on the eleventh, and a relay
// stuck on it stops delivering everything behind it. Parking keeps the row --
// the payload is the only copy of what happened -- and takes it out of the way.
func (o *Outbox) Park(ctx context.Context, id string, cause error) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := o.db.ExecContext(ctx,
		`UPDATE outbox SET failed_at = ?, attempts = attempts + 1, last_error = ? WHERE id = ?`,
		time.Now().UTC(), message, id)
	if err != nil {
		return fmt.Errorf("events: parking %s: %w", id, err)
	}
	return nil
}

// Retry puts a parked event back in line, with its attempt count reset.
//
// The operator fixed the broker, or the consumer, or the payload. Without this
// the only way back is SQL by hand, which is how a dead letter queue becomes a
// table nobody touches.
func (o *Outbox) Retry(ctx context.Context, id string) error {
	_, err := o.db.ExecContext(ctx,
		`UPDATE outbox SET failed_at = NULL, attempts = 0, last_error = NULL WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("events: retrying %s: %w", id, err)
	}
	return nil
}

// Lag is how long the oldest unpublished event has been waiting.
//
// A relay that stopped looks exactly like a relay with nothing to do. This is
// the only number that tells them apart, which is why it feeds the health check
// and the hint on the error page rather than a dashboard.
func (o *Outbox) Lag(ctx context.Context) (time.Duration, error) {
	var oldest sql.NullTime
	err := o.db.QueryRowContext(ctx, `
		SELECT min(occurred_at) FROM outbox
		WHERE published_at IS NULL AND failed_at IS NULL`).Scan(&oldest)
	if err != nil {
		return 0, fmt.Errorf("events: measuring the outbox lag: %w", err)
	}
	if !oldest.Valid {
		return 0, nil
	}
	return time.Since(oldest.Time), nil
}

// MarkPublished records that an event left.
func (o *Outbox) MarkPublished(ctx context.Context, id string) error {
	_, err := o.db.ExecContext(ctx,
		`UPDATE outbox SET published_at = ?, last_error = NULL WHERE id = ?`,
		time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("events: marking %s published: %w", id, err)
	}
	return nil
}

// MarkFailed records an attempt that did not deliver.
//
// The count and the message are stored rather than logged, because the thing
// anyone needs at 3am is "this event failed 12 times with this message", and a
// log line from six hours ago does not answer it.
func (o *Outbox) MarkFailed(ctx context.Context, id string, cause error) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := o.db.ExecContext(ctx,
		`UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
		message, id)
	if err != nil {
		return fmt.Errorf("events: marking %s failed: %w", id, err)
	}
	return nil
}

// Stored is one row of the outbox.
type Stored struct {
	ID           string
	TenantID     string
	Name         string
	Aggregate    string
	AggregateID  string
	Payload      string
	AuthorizedBy string
	Action       string
	OccurredAt   time.Time
	Attempts     int
	// LastError is why the most recent attempt failed. It is stored rather than
	// logged because the thing anyone needs at 3am is "this event failed twelve
	// times with this message", and a log line from six hours ago does not
	// answer it.
	LastError string
}

// Decode unmarshals the payload into v.
func (s Stored) Decode(v any) error {
	if err := json.Unmarshal([]byte(s.Payload), v); err != nil {
		return fmt.Errorf("events: decoding %s: %w", s.Name, err)
	}
	return nil
}
