//go:build !linux

package sharkord

import (
	"context"
	"errors"
)

func FirstStart(context.Context, FirstStartOpts) (string, error) {
	return "", errors.New("Sharkord only runs on Linux")
}
