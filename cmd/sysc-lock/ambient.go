package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/ambient"
)

// runAmbient collects the ambient snapshot once a second until SIGTERM. The
// locker owner spawns it after sealing and kills it on the way out.
func runAmbient() error {
	path := ambient.Path()
	if path == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ambient.Run(ctx, path, time.Second, ambient.NewGather())
}
