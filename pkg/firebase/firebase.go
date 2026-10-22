// Package firebase is a thin wrapper around the Firebase Admin SDK,
// scoped to what the Tuai backend actually needs: verifying ID tokens
// produced by the FE Google sign-in flow.
//
// Keeping the SDK behind this wrapper means callers (the auth service)
// don't import firebase-admin types directly — only the small struct
// returned by VerifyIDToken — which keeps the rest of the codebase
// independent of the SDK's version churn.
package firebase

import (
	"context"
	"errors"
	"fmt"

	firebaseapp "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// Client wraps the Firebase Auth client. It is safe for concurrent use.
type Client struct {
	auth *auth.Client
}

// VerifiedToken is the slice of a verified Firebase ID token that the
// auth service consumes. We map the SDK's auth.Token down to this so
// the auth package stays SDK-free.
type VerifiedToken struct {
	// UID is the Firebase user id (claim "user_id" / "sub"). Stable
	// across sessions and across email changes — use it as the primary
	// key in core.user_identities.
	UID string
	// Email is the email reported by Firebase. Empty if the provider
	// did not return one (Google always does).
	Email string
	// EmailVerified mirrors the Firebase claim. For Google it is true
	// because Google itself verifies emails before issuing identity
	// tokens.
	EmailVerified bool
	// Name is the user's full name from the provider. Used to seed
	// core.users.full_name.
	Name string
	// Picture is the avatar URL from the provider.
	Picture string
	// SignInProvider is the underlying provider Firebase used
	// (e.g. "google.com"). The Tuai service rejects anything that
	// isn't an expected provider so a Firebase token issued via an
	// unintended path (custom auth, email link, etc.) cannot bypass
	// the Google flow.
	SignInProvider string
	// Claims is the full claim set. Stored verbatim in
	// core.user_identities.raw_profile for forensic / re-sync purposes.
	Claims map[string]any
}

// New initializes a Firebase Admin client.
//
//   - projectID is mandatory; if empty, VerifyIDToken would silently
//     accept tokens issued for the wrong project, so we refuse to
//     construct a Client without it.
//   - credentialsJSON is the raw service-account JSON. If empty, the
//     SDK falls back to Application Default Credentials, which is the
//     right thing inside GCP (workload identity).
func New(ctx context.Context, projectID, credentialsJSON string) (*Client, error) {
	if projectID == "" {
		return nil, errors.New("firebase: project id is required")
	}

	cfg := &firebaseapp.Config{ProjectID: projectID}

	var opts []option.ClientOption
	if credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credentialsJSON)))
	}

	app, err := firebaseapp.NewApp(ctx, cfg, opts...)
	if err != nil {
		return nil, fmt.Errorf("firebase: init app: %w", err)
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase: init auth client: %w", err)
	}
	return &Client{auth: authClient}, nil
}

// VerifyIDToken validates a Firebase ID token and returns the bits the
// auth service needs. The SDK call already checks signature, issuer,
// audience (= configured project), and expiry — anything that returns
// here is safe to act on.
func (c *Client) VerifyIDToken(ctx context.Context, idToken string) (*VerifiedToken, error) {
	tok, err := c.auth.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, err
	}
	return mapToken(tok), nil
}

// mapToken pulls the well-known string claims out of the Firebase
// token map and returns a typed view. Missing / wrong-typed values
// degrade to zero values rather than failing the verification — the
// caller decides which fields are mandatory for its flow.
func mapToken(tok *auth.Token) *VerifiedToken {
	out := &VerifiedToken{
		UID:    tok.UID,
		Claims: tok.Claims,
	}
	if v, ok := tok.Claims["email"].(string); ok {
		out.Email = v
	}
	if v, ok := tok.Claims["email_verified"].(bool); ok {
		out.EmailVerified = v
	}
	if v, ok := tok.Claims["name"].(string); ok {
		out.Name = v
	}
	if v, ok := tok.Claims["picture"].(string); ok {
		out.Picture = v
	}
	// Firebase nests provider info under tok.Firebase or as a "firebase"
	// claim map. Prefer the typed accessor, fall back to the claims map.
	if tok.Firebase.SignInProvider != "" {
		out.SignInProvider = tok.Firebase.SignInProvider
	} else if fb, ok := tok.Claims["firebase"].(map[string]any); ok {
		if sp, ok := fb["sign_in_provider"].(string); ok {
			out.SignInProvider = sp
		}
	}
	return out
}
