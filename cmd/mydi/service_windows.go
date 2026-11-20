//go:build windows

package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
)

// hostService hands the process to the service control manager as the service `name` and runs `run` until it ends or the service is
// told to stop: the timing measure as a service gets its priority and class (ADR-0003 part C).
func hostService(name string, run func(ctx context.Context) error) error {
	return svc.Run(name, &host{run: run})
}

type host struct {
	run func(ctx context.Context) error
}

// Execute is the manager's side of the service: told to stop, it cancels the measure and waits for it up to 20 s.
func (h *host) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const wait = 20 * time.Second
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: uint32(wait.Milliseconds())}
				cancel()
				select {
				case <-ended:
				case <-time.After(wait):
				}
				return false, 0
			}
		case err := <-ended:
			if err != nil {
				fmt.Printf("mydi: %v\n", err)
				return true, 1
			}
			return false, 0
		}
	}
}
