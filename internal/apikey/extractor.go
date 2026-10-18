package apikey

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrAPIKeyMissing = errors.New("API key required")
	ErrAPIKeyInvalid = errors.New("API key format invalid")
)

const (
	HeaderAPIKey    = "X-API-Key"
	MaxAPIKeyLength = 128
)

// Extract returns the API key present in the X-API-Key header or an error (D1, D14)
func Extract(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get(HeaderAPIKey))
	if key == "" {
		return "", ErrAPIKeyMissing
	}

	if len(key) > MaxAPIKeyLength {
		return "", ErrAPIKeyInvalid
	}

	for _, ch := range key {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' && ch != '.' && ch != ':' {
			return "", ErrAPIKeyInvalid
		}
	}

	return key, nil
}
