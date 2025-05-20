package gosmee

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNewHookURL_Success(t *testing.T) {
	expectedURL := "https://example.com/redirected"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		fmt.Fprint(w, expectedURL)
	}))
	defer server.Close()

	output, err := getNewHookURL(server.URL)
	if err != nil {
		t.Errorf("getNewHookURL() error = %v", err)
	}
	if output != expectedURL {
		t.Errorf("getNewHookURL() output = %q, want %q", output, expectedURL)
	}
}
