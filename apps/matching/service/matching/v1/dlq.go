package v1

import (
	"context"

	util "github.com/pitabwire/util"
)

// DeadLetterPublisher is the subset of an EventsManager we need to drop a
// poisoned payload onto the dead-letter subject.
type DeadLetterPublisher interface {
	Publish(ctx context.Context, subject string, payload []byte) error
}

// DLQGuard publishes payloads to a dead-letter subject after a configurable
// number of failed redeliveries. The original consumer should call Run
// inside its Handle method.
type DLQGuard struct {
	pub       DeadLetterPublisher
	subject   string
	threshold int
}

// NewDLQGuard constructs a guard. threshold is the redelivery count
// at which the next failure dead-letters the message. Spec §3.7 sets it
// to 5.
func NewDLQGuard(pub DeadLetterPublisher, subject string, threshold int) *DLQGuard {
	if threshold < 1 {
		threshold = 5
	}
	return &DLQGuard{pub: pub, subject: subject, threshold: threshold}
}

// Run invokes work() and inspects the result. On success: returns nil.
// On error with redelivery < threshold: returns the error so JetStream
// will redeliver. On error with redelivery >= threshold: publishes the
// payload to the DLQ subject and returns nil so JetStream ACKs the
// message (stopping the redelivery loop).
func (g *DLQGuard) Run(ctx context.Context, redelivery int, payload []byte, work func() error) error {
	err := work()
	if err == nil {
		return nil
	}
	if redelivery < g.threshold {
		return err
	}
	if pubErr := g.pub.Publish(ctx, g.subject, payload); pubErr != nil {
		util.Log(ctx).WithError(pubErr).WithField("subject", g.subject).
			Error("dlq publish failed; falling back to redelivery")
		return err
	}
	util.Log(ctx).WithError(err).WithField("subject", g.subject).
		WithField("redelivery", redelivery).
		Warn("dead-lettered after redelivery budget exhausted")
	return nil
}
