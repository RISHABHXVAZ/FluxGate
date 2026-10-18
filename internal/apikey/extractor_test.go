package apikey_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"fluxgate/internal/apikey"
)

func TestExtract_ValidKeys(t *testing.T) {
	validKeys := []string{
		"client-alpha",
		"client_vip",
		"user.123:test",
		"DEFAULT",
		"abc-123_456.xyz",
	}

	for _, k := range validKeys {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-API-Key", k)

		extracted, err := apikey.Extract(req)
		if err != nil {
			t.Errorf("expected key %q to be valid, got error: %v", k, err)
		}
		if extracted != k {
			t.Errorf("expected %q, got %q", k, extracted)
		}
	}
}

func TestExtract_MissingKey(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	_, err := apikey.Extract(req)
	if err != apikey.ErrAPIKeyMissing {
		t.Errorf("expected ErrAPIKeyMissing, got %v", err)
	}

	req.Header.Set("X-API-Key", "   ")
	_, err = apikey.Extract(req)
	if err != apikey.ErrAPIKeyMissing {
		t.Errorf("expected ErrAPIKeyMissing on blank string, got %v", err)
	}
}

func TestExtract_InvalidKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"excessive_length", strings.Repeat("a", 129)},
		{"semicolon_injection", "client;DROP TABLE"},
		{"spaces_inside", "client alpha"},
		{"null_byte", "client\x00alpha"},
		{"special_chars", "client$<script>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-API-Key", tc.key)

			_, err := apikey.Extract(req)
			if err != apikey.ErrAPIKeyInvalid {
				t.Errorf("expected ErrAPIKeyInvalid for %s, got %v", tc.name, err)
			}
		})
	}
}
