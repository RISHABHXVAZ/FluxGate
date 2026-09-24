package apikey

import (
	"errors"
	"net/http"
	"strings"
)

var ErrAPIKeyMissing = errors.New("API key required")

const HeaderAPIKey = "X-API-Key"

// Extract returns the API key present in the X-API-Key header or ErrAPIKeyMissing (D1, D14)
func Extract(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get(HeaderAPIKey))
	if key == "" {
		return "", ErrAPIKeyMissing
	}
	return key, nil
}
