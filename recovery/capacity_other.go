//go:build !linux && !darwin

package recovery

import "context"

func (e *Engine) checkCapacity(context.Context, *Operation) error {
	return ErrUnsupportedPlatform
}
