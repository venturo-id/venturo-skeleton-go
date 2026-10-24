package authz

// APIKeyPrefix starts every API key: tbx_<env>_<key_id>_<secret>. Changing
// it invalidates every key already issued.
const APIKeyPrefix = "tbx"

// CredentialError means a presented credential (e.g. an API key) was
// rejected. Reason is safe to send to the client; Err keeps the precise
// cause for logs and errors.Is.
//
// Anything else returned while checking a credential is an internal
// failure and must not be shown to the client.
type CredentialError struct {
	Reason string
	Err    error
}

func (e *CredentialError) Error() string { return e.Err.Error() }

func (e *CredentialError) Unwrap() error { return e.Err }
