package robot

import (
	"context"

	"openclaw-guard-kit/internal/protocol"
)

type Bot interface {
	Name() string
	Start(context.Context) error
	Stop(context.Context) error
	Publish(context.Context, protocol.Event) error
}

type Hub interface {
	Register(Bot)
	StartAll(context.Context) error
	StopAll(context.Context) error
	Broadcast(context.Context, protocol.Event) error
}
