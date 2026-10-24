package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	ctx := WithActor(WithClient(context.Background(), "203.0.113.7", "curl/8"), "user-1", "key-1")

	e := complete(ctx, Entry{Action: ActionLogout})
	if e.At.IsZero() || e.IP != "203.0.113.7" || e.UserAgent != "curl/8" || e.ActorID != "user-1" || e.APIKeyID != "key-1" {
		t.Errorf("complete() = %+v", e)
	}

	// What the caller set wins.
	e = complete(ctx, Entry{Action: ActionSignIn, ActorID: "user-2", IP: "198.51.100.1"})
	if e.ActorID != "user-2" || e.APIKeyID != "" || e.IP != "198.51.100.1" {
		t.Errorf("complete() overrode the caller: %+v", e)
	}

	long := strings.Repeat("é", maxUserAgent)
	e = complete(WithClient(context.Background(), "", long), Entry{})
	if len(e.UserAgent) > maxUserAgent || !strings.HasPrefix(long, e.UserAgent) {
		t.Errorf("user agent kept %d bytes, want at most %d of a valid prefix", len(e.UserAgent), maxUserAgent)
	}
}

func TestActorID(t *testing.T) {
	if got := ActorID(context.Background()); got != "" {
		t.Errorf("ActorID(no actor) = %q", got)
	}
	if got := ActorID(WithActor(context.Background(), "user-1", "")); got != "user-1" {
		t.Errorf("ActorID() = %q, want user-1", got)
	}
}

func TestDiscard(t *testing.T) {
	log, err := Open(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Open(no URI) error = %v", err)
	}
	if _, ok := log.(Discard); !ok {
		t.Fatalf("Open(no URI) = %T, want Discard", log)
	}
	log.Record(context.Background(), Entry{Action: ActionSignIn})
	if _, _, err := log.List(context.Background(), Filter{}, 1, 10); !errors.Is(err, ErrDisabled) {
		t.Errorf("List() err = %v, want ErrDisabled", err)
	}
	if entries, err := log.ListByActor(context.Background(), "user-1", 10); err != nil || len(entries) != 0 {
		t.Errorf("ListByActor() = %v, %v; want none", entries, err)
	}
}
