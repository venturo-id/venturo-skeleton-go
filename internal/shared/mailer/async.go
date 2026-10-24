// Package mailer sends the application's emails in the background.
//
// A request never waits on SMTP, and — just as important for the
// forgot-password and resend-verification endpoints — a response takes
// the same time whether or not an email went out, so timing does not
// reveal which accounts exist.
package mailer

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"timebox-be/pkg/email"
	"timebox-be/pkg/logger"
)

const (
	// maxInFlight bounds how many emails are handed to SMTP at once; the
	// rest wait their turn instead of opening more connections.
	maxInFlight = 8
	// maxPending bounds the emails sending or waiting for a slot. A stalled
	// SMTP server holds every slot for up to its send timeout (30s); past
	// this many, new emails are dropped rather than piling up goroutines.
	maxPending = 256
)

// Async wraps an email.EmailService so every send returns at once and
// runs in the background. A failed or dropped send is logged, never
// returned.
type Async struct {
	inner email.EmailService
	slots chan struct{}

	mu      sync.Mutex
	pending int
	// idle is closed when pending drops to 0, and replaced when it rises
	// from 0. Unlike a WaitGroup, it lets a send start while Wait runs —
	// as it may when shutdown cut requests short.
	idle    chan struct{}
	dropped atomic.Int64
}

var _ email.EmailService = (*Async)(nil)

// NewAsync wraps inner.
func NewAsync(inner email.EmailService) *Async {
	return &Async{inner: inner, slots: make(chan struct{}, maxInFlight)}
}

// dispatch runs send in the background, or drops it when maxPending
// emails are already waiting. It never blocks the caller.
func (a *Async) dispatch(kind string, send func() error) {
	a.mu.Lock()
	if a.pending >= maxPending {
		a.mu.Unlock()
		// Logged on the first drop and every 100th after it. The kind
		// only: never the recipient or the code.
		if n := a.dropped.Add(1); n%100 == 1 {
			logger.Error("Email queue is full; emails dropped",
				logger.String("kind", kind), logger.Int64("dropped", n))
		}
		return
	}
	if a.pending == 0 {
		a.idle = make(chan struct{})
	}
	a.pending++
	a.mu.Unlock()

	go func() {
		defer a.done()
		a.slots <- struct{}{}
		defer func() { <-a.slots }()

		if err := send(); err != nil {
			logger.Error("Email not sent", logger.String("kind", kind), logger.Err(err))
		}
	}()
}

func (a *Async) done() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending--
	if a.pending == 0 {
		close(a.idle)
	}
}

// Wait blocks until every queued email has been handed to SMTP, or ctx
// ends. Call it on shutdown, after the HTTP server stopped taking requests.
func (a *Async) Wait(ctx context.Context) error {
	a.mu.Lock()
	if a.pending == 0 {
		a.mu.Unlock()
		return nil
	}
	idle := a.idle
	a.mu.Unlock()

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Async) SendVerificationCode(to, name, code string, validFor time.Duration) error {
	a.dispatch("verification", func() error { return a.inner.SendVerificationCode(to, name, code, validFor) })
	return nil
}

func (a *Async) SendPasswordResetCode(to, name, code string, validFor time.Duration) error {
	a.dispatch("password_reset", func() error { return a.inner.SendPasswordResetCode(to, name, code, validFor) })
	return nil
}

func (a *Async) SendEmailChangeCode(to, name, code string, validFor time.Duration) error {
	a.dispatch("email_change", func() error { return a.inner.SendEmailChangeCode(to, name, code, validFor) })
	return nil
}

func (a *Async) SendWelcomeEmail(to, name string) error {
	a.dispatch("welcome", func() error { return a.inner.SendWelcomeEmail(to, name) })
	return nil
}

func (a *Async) SendAccountLockedEmail(to, name string, lockedFor time.Duration) error {
	a.dispatch("account_locked", func() error { return a.inner.SendAccountLockedEmail(to, name, lockedFor) })
	return nil
}

func (a *Async) SendPasswordChangedEmail(to, name string) error {
	a.dispatch("password_changed", func() error { return a.inner.SendPasswordChangedEmail(to, name) })
	return nil
}

func (a *Async) SendEmailChangedEmail(to, name, newEmail string) error {
	a.dispatch("email_changed", func() error { return a.inner.SendEmailChangedEmail(to, name, newEmail) })
	return nil
}

func (a *Async) SendAccountClosedEmail(to, name string) error {
	a.dispatch("account_closed", func() error { return a.inner.SendAccountClosedEmail(to, name) })
	return nil
}

func (a *Async) SendGoogleLinkedEmail(to, name, googleEmail string) error {
	a.dispatch("google_linked", func() error { return a.inner.SendGoogleLinkedEmail(to, name, googleEmail) })
	return nil
}

func (a *Async) SendGoogleUnlinkedEmail(to, name, googleEmail string) error {
	a.dispatch("google_unlinked", func() error { return a.inner.SendGoogleUnlinkedEmail(to, name, googleEmail) })
	return nil
}
