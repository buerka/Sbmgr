package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const configurationCommandTimeout = 30 * time.Second

// Each validation, application and rollback command gets its own budget, so
// a stalled command cannot hold the state lock indefinitely or consume the
// recovery step's timeout. WaitDelay also bounds inherited output pipes.
func runConfigurationCommand(stdout, stderr io.Writer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), configurationCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}
