// Package payment models a multi-step payment saga: steps run sequentially,
// each step knows how to compensate itself, and a state machine drives the workflow.
package payment

import (
	"errors"
	"fmt"
	"strings"
)

// Errors for invalid transitions and inputs.
var (
	ErrInvalidStatus     = errors.New("payment: invalid status")
	ErrInvalidTransition = errors.New("payment: invalid state transition")
	ErrInvalidStep       = errors.New("payment: invalid step")
	ErrNoStepsRemaining  = errors.New("payment: no steps remaining")
	ErrTerminal          = errors.New("payment: payment is in a terminal state")
)

// Status tracks where the payment is in its saga lifecycle.
type Status int

const (
	// Pending means created but not yet claimed by a worker.
	Pending Status = iota + 1

	// InProgress means at least one step executed and more steps remain.
	InProgress

	// Settled is success: all steps completed.
	Settled

	// Compensating means a step failed and completed steps are being reversed.
	Compensating

	// Compensated means the payment failed and all finished steps were undone.
	Compensated

	// CompensationFailed means unwinding failed and needs manual intervention.
	CompensationFailed
)

// AllStatuses returns every status to help tests be exhaustive.
func AllStatuses() []Status {
	return []Status{Pending, InProgress, Settled, Compensating, Compensated, CompensationFailed}
}

// IsTerminal reports whether the payment reached an end state.
func (s Status) IsTerminal() bool {
	return s == Settled || s == Compensated || s == CompensationFailed
}

// IsValid reports whether s is a valid status enum value.
func (s Status) IsValid() bool { return s >= Pending && s <= CompensationFailed }

// String returns the uppercase status name used in the database and API.
func (s Status) String() string {
	switch s {
	case Pending:
		return "PENDING"
	case InProgress:
		return "IN_PROGRESS"
	case Settled:
		return "SETTLED"
	case Compensating:
		return "COMPENSATING"
	case Compensated:
		return "COMPENSATED"
	case CompensationFailed:
		return "COMPENSATION_FAILED"
	default:
		return "INVALID"
	}
}

// ParseStatus converts a status string to a Status enum.
func ParseStatus(s string) (Status, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING":
		return Pending, nil
	case "IN_PROGRESS":
		return InProgress, nil
	case "SETTLED":
		return Settled, nil
	case "COMPENSATING":
		return Compensating, nil
	case "COMPENSATED":
		return Compensated, nil
	case "COMPENSATION_FAILED":
		return CompensationFailed, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidStatus, s)
	}
}

// StepStatus tracks the execution state of an individual step.
type StepStatus int

const (
	// StepPending means the step has not run yet.
	StepPending StepStatus = iota + 1

	// StepDone means the step completed successfully and has effects to undo if needed.
	StepDone

	// StepCompensated means the step was undone.
	StepCompensated

	// StepFailed means the step failed execution, leaving nothing to undo.
	StepFailed
)

// IsValid reports whether s is a valid step status enum value.
func (s StepStatus) IsValid() bool { return s >= StepPending && s <= StepFailed }

// String returns the uppercase step status name.
func (s StepStatus) String() string {
	switch s {
	case StepPending:
		return "PENDING"
	case StepDone:
		return "DONE"
	case StepCompensated:
		return "COMPENSATED"
	case StepFailed:
		return "FAILED"
	default:
		return "INVALID"
	}
}

// ParseStepStatus converts a string into a StepStatus enum.
func ParseStepStatus(s string) (StepStatus, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING":
		return StepPending, nil
	case "DONE":
		return StepDone, nil
	case "COMPENSATED":
		return StepCompensated, nil
	case "FAILED":
		return StepFailed, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidStatus, s)
	}
}
