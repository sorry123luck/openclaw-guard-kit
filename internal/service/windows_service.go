//go:build windows

package service

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

func Run(name string, run RunFunc) error {
	return svc.Run(name, &serviceHandler{run: run})
}

type serviceHandler struct {
	run RunFunc
}

func (h *serviceHandler) Execute(_ []string, changes <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown

	statuses <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- h.run(ctx)
	}()

	current := svc.Status{
		State:   svc.Running,
		Accepts: accepts,
	}
	statuses <- current

	for {
		select {
		case change := <-changes:
			switch change.Cmd {
			case svc.Interrogate:
				statuses <- current

			case svc.Stop, svc.Shutdown:
				current = svc.Status{State: svc.StopPending}
				statuses <- current
				cancel()

			default:
			}

		case err := <-errCh:
			statuses <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
