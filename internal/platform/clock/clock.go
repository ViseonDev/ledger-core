// Package clock provides the time source the application depends on.
package clock

import "time"

// System reads the real clock.
type System struct{}

// New returns the system clock.
func New() System { return System{} }

// Now implements ports.Clock.
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a clock that always reports the same instant, for tests that need
// deterministic timestamps.
type Fixed struct {
	Instant time.Time
}

// Now implements ports.Clock.
func (f Fixed) Now() time.Time { return f.Instant.UTC() }
