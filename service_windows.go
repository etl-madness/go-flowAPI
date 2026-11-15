//go:build windows

package main

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

type windowsServiceHandler struct {
	cfg *ServerConfig
}

func (h *windowsServiceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const acceptedCmds = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyCh := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		errCh <- runAPIServer(ctx, h.cfg, func() {
			close(readyCh)
		})
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("[go-flowAPI] Windows service startup failed: %v", err)
			status <- svc.Status{State: svc.Stopped}
			return true, 1
		}
		status <- svc.Status{State: svc.Stopped}
		return false, 0
	case <-readyCh:
		status <- svc.Status{State: svc.Running, Accepts: acceptedCmds}
	}

	for {
		select {
		case err := <-errCh:
			status <- svc.Status{State: svc.Stopped}
			if err != nil {
				log.Printf("[go-flowAPI] Windows service stopped with error: %v", err)
				return true, 1
			}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-errCh
				status <- svc.Status{State: svc.Stopped}
				if err != nil {
					log.Printf("[go-flowAPI] Windows service shutdown error: %v", err)
					return true, 1
				}
				return false, 0
			default:
			}
		}
	}
}

func maybeRunOSService(cfg *ServerConfig) (bool, error) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false, err
	}
	if !isSvc {
		return false, nil
	}
	log.Printf("[go-flowAPI] starting under Windows Service Control Manager (service=%q)", cfg.ServiceName)
	return true, svc.Run(cfg.ServiceName, &windowsServiceHandler{cfg: cfg})
}
