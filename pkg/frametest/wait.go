// Package frametest contains helpers used only by tests that wire up
// a real *frame.Service. They keep test code out of the production
// binaries without forcing each test package to redeclare the same
// boilerplate.
package frametest

import (
	"testing"
	"time"

	"github.com/pitabwire/frame"
)

// frameInternalEventsQueue is the default queue name Frame registers
// internally when setting up the events bus (see frame/options_events.go
// setupEventsQueue). Tests that only use svc.EventsManager().Emit() and
// do not register explicit queue publishers still need to wait for this
// publisher to be ready before emitting.
const frameInternalEventsQueue = "frame.events.internal_._queue"

// WaitPublisherReady polls svc.QueueManager().GetPublisher(topic).Initiated()
// until it returns true. The check is needed because Frame's
// (*Service).Run() initializes registered publishers in a goroutine,
// and a plain time.Sleep() does not establish the Go-memory-model
// happens-before that publisher.Publish() needs when reading
// publisher.topic. The Initiated() call is backed by an atomic.Bool,
// so polling it provides the missing synchronization.
//
// If topic is not registered as a queue publisher, WaitPublisherReady
// falls back to waiting for the Frame-internal events queue publisher
// (the transport used by EventsManager.Emit). This covers tests that
// emit via EventsManager without registering an explicit queue publisher.
//
// Call this in tests that:
//
//	go func() { _ = svc.Run(ctx, "") }()
//	frametest.WaitPublisherReady(t, svc, eventsv1.TopicXxx, 2*time.Second)
//	// … now safe to Emit / Publish without -race screaming.
func WaitPublisherReady(t *testing.T, svc *frame.Service, topic string, timeout time.Duration) {
	t.Helper()
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pub, err := svc.QueueManager().GetPublisher(topic)
		if err == nil && pub != nil && pub.Initiated() {
			return
		}
		// Fall back: if the requested topic is not a registered queue
		// publisher, wait for the internal events queue publisher instead.
		if topic != frameInternalEventsQueue {
			if fb, fbErr := svc.QueueManager().GetPublisher(frameInternalEventsQueue); fbErr == nil && fb != nil && fb.Initiated() {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("frametest: publisher %q not ready within %s", topic, timeout)
}
