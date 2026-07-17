package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pitabwire/frame/v2"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// FrameEmbedPublisher publishes OpportunityEmbedV1 onto the Frame Queue
// subject SubjectWorkerEmbed (reference must match WithRegisterPublisher).
type FrameEmbedPublisher struct {
	svc *frame.Service
	ref string
}

// NewFrameEmbedPublisher builds a publisher. ref is the queue reference
// (usually eventsv1.SubjectWorkerEmbed).
func NewFrameEmbedPublisher(svc *frame.Service, ref string) *FrameEmbedPublisher {
	if ref == "" {
		ref = eventsv1.SubjectWorkerEmbed
	}
	return &FrameEmbedPublisher{svc: svc, ref: ref}
}

// PublishEmbed implements EmbedPublisher.
func (p *FrameEmbedPublisher) PublishEmbed(ctx context.Context, job eventsv1.OpportunityEmbedV1) error {
	if p == nil || p.svc == nil || p.svc.QueueManager() == nil {
		return fmt.Errorf("embed publisher: queue manager not configured")
	}
	env := eventsv1.NewEnvelope(eventsv1.SubjectWorkerEmbed, job)
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("embed publisher: marshal: %w", err)
	}
	return p.svc.QueueManager().Publish(ctx, p.ref, body)
}

// FrameFanOutPublisher publishes OpportunityFanOutV1 for matching Path A.
type FrameFanOutPublisher struct {
	svc *frame.Service
	ref string
}

// NewFrameFanOutPublisher builds a publisher for SubjectOpportunityFanOut.
func NewFrameFanOutPublisher(svc *frame.Service, ref string) *FrameFanOutPublisher {
	if ref == "" {
		ref = eventsv1.SubjectOpportunityFanOut
	}
	return &FrameFanOutPublisher{svc: svc, ref: ref}
}

// PublishFanOut implements FanOutPublisher.
func (p *FrameFanOutPublisher) PublishFanOut(ctx context.Context, job eventsv1.OpportunityFanOutV1) error {
	if p == nil || p.svc == nil || p.svc.QueueManager() == nil {
		return fmt.Errorf("fanout publisher: queue manager not configured")
	}
	env := eventsv1.NewEnvelope(eventsv1.SubjectOpportunityFanOut, job)
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("fanout publisher: marshal: %w", err)
	}
	return p.svc.QueueManager().Publish(ctx, p.ref, body)
}
