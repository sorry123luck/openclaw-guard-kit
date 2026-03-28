package robot

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
)

type NoopBot struct {
	name string
}

func NewNoopBot() *NoopBot {
	return &NoopBot{name: "noop"}
}

func (b *NoopBot) Name() string {
	return b.name
}

func (b *NoopBot) Start(context.Context) error {
	return nil
}

func (b *NoopBot) Stop(context.Context) error {
	return nil
}

func (b *NoopBot) Publish(context.Context, protocol.Event) error {
	return nil
}
