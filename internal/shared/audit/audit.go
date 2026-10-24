// Package audit records who did what to whom: sign-ins, credential and
// session changes, and changes to users, roles and API keys. The log is
// kept in MongoDB, away from the application data it describes, and
// entries expire after AUDIT_LOG_RETENTION.
//
// Recording never blocks or fails a request: entries are queued and
// written in batches in the background (see Store). Without MONGO_URI the
// log is Discard and records nothing.
//
// An entry names people by user id only — never by email, name or the
// login someone typed. It does keep the client's IP address and user
// agent, as a security record, until it expires: closing an account does
// not remove its entries.
package audit

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Actions. "<area>.<what happened>", past tense.
const (
	ActionSignUp               = "auth.sign_up"
	ActionSignIn               = "auth.sign_in"
	ActionSignInRefused        = "auth.sign_in_refused"
	ActionWrongPassword        = "auth.wrong_password"
	ActionAccountLocked        = "auth.account_locked"
	ActionRefreshTokenReused   = "auth.refresh_token_reused"
	ActionLogout               = "auth.logout"
	ActionLogoutAll            = "auth.logout_all"
	ActionSessionEnded         = "auth.session_ended"
	ActionPasswordChanged      = "auth.password_changed"
	ActionPasswordReset        = "auth.password_reset"
	ActionEmailVerified        = "auth.email_verified"
	ActionEmailChangeRequested = "auth.email_change_requested"
	ActionEmailChanged         = "auth.email_changed"
	ActionAccountClaimed       = "auth.account_claimed"
	ActionAccountClosed        = "auth.account_closed"
	ActionGoogleLinked         = "auth.google_linked"
	ActionGoogleUnlinked       = "auth.google_unlinked"

	ActionUserCreated   = "user.created"
	ActionUserUpdated   = "user.updated"
	ActionUserDeleted   = "user.deleted"
	ActionAvatarChanged = "user.avatar_changed"
	ActionAvatarRemoved = "user.avatar_removed"

	ActionRoleCreated = "role.created"
	ActionRoleUpdated = "role.updated"
	ActionRoleDeleted = "role.deleted"

	ActionAPIKeyCreated = "api_key.created"
	ActionAPIKeyUpdated = "api_key.updated"
	ActionAPIKeyRevoked = "api_key.revoked"
)

// Target types. Session actions target the user; the session is in
// details.session_id.
const (
	TargetUser   = "user"
	TargetRole   = "role"
	TargetAPIKey = "api_key"
)

// Entry is one audit log record.
type Entry struct {
	ID     bson.ObjectID `bson:"_id,omitempty" json:"id"`
	At     time.Time     `bson:"at" json:"at"`
	Action string        `bson:"action" json:"action"`
	// ActorID is the user who acted; empty when nobody is signed in, e.g.
	// a sign-in with an unknown login. APIKeyID is set when they acted
	// through an API key: the key's key_id (the part after tbx_<env>_ that
	// its key_prefix shows), not its UUID.
	ActorID  string `bson:"actor_id,omitempty" json:"actor_id,omitempty"`
	APIKeyID string `bson:"api_key_id,omitempty" json:"api_key_id,omitempty"`
	// TargetType and TargetID name what was acted on.
	TargetType string `bson:"target_type,omitempty" json:"target_type,omitempty"`
	TargetID   string `bson:"target_id,omitempty" json:"target_id,omitempty"`
	IP         string `bson:"ip,omitempty" json:"ip,omitempty"`
	UserAgent  string `bson:"user_agent,omitempty" json:"user_agent,omitempty"`
	// Details are facts about the action — a reason, the names of the
	// fields that changed — never personal data or secrets.
	Details map[string]any `bson:"details,omitempty" json:"details,omitempty"`
}

// Recorder records audit entries. Record returns at once and never fails
// the caller; At, the actor and the client fields are filled from ctx when
// left empty (see WithClient, WithActor).
type Recorder interface {
	Record(ctx context.Context, e Entry)
}

// Filter narrows List. Empty fields match everything.
type Filter struct {
	ActorID    string
	TargetType string
	TargetID   string
	Action     string
	From, To   *time.Time
}

// Reader reads the log, newest first.
type Reader interface {
	List(ctx context.Context, f Filter, page, limit int) ([]Entry, int64, error)
	// ListByActor returns at most limit entries of what userID did.
	ListByActor(ctx context.Context, userID string, limit int) ([]Entry, error)
}

// Log is the whole audit log. Close flushes queued entries; call it on
// shutdown, after the HTTP server stopped taking requests.
type Log interface {
	Recorder
	Reader
	Close(ctx context.Context) error
}

// ErrDisabled is what reading a Discard log returns.
var ErrDisabled = errors.New("audit log is not configured")

// Discard is the log without MONGO_URI: it records nothing, and reading it
// fails with ErrDisabled — except ListByActor, which finds nothing.
type Discard struct{}

var _ Log = Discard{}

func (Discard) Record(context.Context, Entry) {}

func (Discard) List(context.Context, Filter, int, int) ([]Entry, int64, error) {
	return nil, 0, ErrDisabled
}

func (Discard) ListByActor(context.Context, string, int) ([]Entry, error) {
	return []Entry{}, nil
}

func (Discard) Close(context.Context) error { return nil }

// ─── Request context ─────────────────────────────────────────────────

type clientKey struct{}
type actorKey struct{}

type client struct{ ip, userAgent string }
type actor struct{ userID, apiKeyID string }

// WithClient returns ctx carrying the requesting client's IP address and
// user agent.
func WithClient(ctx context.Context, ip, userAgent string) context.Context {
	return context.WithValue(ctx, clientKey{}, client{ip: ip, userAgent: userAgent})
}

// WithActor returns ctx carrying the authenticated caller, and the API
// key they used, if any.
func WithActor(ctx context.Context, userID, apiKeyID string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor{userID: userID, apiKeyID: apiKeyID})
}

// ActorID returns the authenticated caller ctx carries, or "".
func ActorID(ctx context.Context) string {
	a, _ := ctx.Value(actorKey{}).(actor)
	return a.userID
}

// maxUserAgent bounds the stored user agent; the header has no limit of
// its own below the request's.
const maxUserAgent = 512

// complete fills what e leaves empty from ctx and the clock.
func complete(ctx context.Context, e Entry) Entry {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if c, ok := ctx.Value(clientKey{}).(client); ok {
		if e.IP == "" {
			e.IP = c.ip
		}
		if e.UserAgent == "" {
			e.UserAgent = c.userAgent
		}
	}
	if a, ok := ctx.Value(actorKey{}).(actor); ok && e.ActorID == "" {
		e.ActorID, e.APIKeyID = a.userID, a.apiKeyID
	}
	if len(e.UserAgent) > maxUserAgent {
		e.UserAgent = strings.ToValidUTF8(e.UserAgent[:maxUserAgent], "")
	}
	return e
}
