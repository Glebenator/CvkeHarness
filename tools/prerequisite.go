package tools

import "errors"

// PrerequisiteError cannot be fixed by rephrasing the same model tool call.
// Correctable argument/schema errors must not use this type.
type PrerequisiteError struct{ Reason string }

func (e PrerequisiteError) Error() string { return e.Reason }
func IsPrerequisiteError(err error) bool  { var e PrerequisiteError; return errors.As(err, &e) }
