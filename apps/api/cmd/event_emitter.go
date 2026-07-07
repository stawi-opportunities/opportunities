package main

import (
	"context"
	"errors"

	"github.com/pitabwire/frame"
)

// frameEmitter is used only for control-plane commands and definition change
// notifications; job records themselves are never transported through it.
type frameEmitter struct{ svc *frame.Service }

func (f frameEmitter) Emit(ctx context.Context, topic string, payload any) error {
	if f.svc == nil || f.svc.EventsManager() == nil {
		return errors.New("events manager unavailable")
	}
	return f.svc.EventsManager().Emit(ctx, topic, payload)
}
