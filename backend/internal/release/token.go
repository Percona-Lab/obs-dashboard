package release

import "context"

// TokenSource supplies the bearer token for one GitHub request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource for a fixed token (a personal access token).
type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }
