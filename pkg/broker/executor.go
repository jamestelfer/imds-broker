package broker

import (
	"context"
	"os/exec"
)

// CommandExecutor runs an external command and returns its combined output.
type CommandExecutor interface {
	Execute(ctx context.Context, name string, args ...string) ([]byte, error)
}

// OSExecutor is the production CommandExecutor backed by os/exec.
type OSExecutor struct{}

func (OSExecutor) Execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
