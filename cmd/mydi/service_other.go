//go:build !windows

package main

import (
	"context"
	"errors"
)

// hostService is Windows's alone: elsewhere the measure runs as any program does.
func hostService(string, func(ctx context.Context) error) error {
	return errors.New("--service is for Windows's service control manager")
}
