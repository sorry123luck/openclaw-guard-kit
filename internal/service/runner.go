package service

import (
	"context"
	"os"
	"os/signal"
)

type RunFunc func(ctx context.Context) error

func RunConsole(run RunFunc) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return run(ctx)
}
