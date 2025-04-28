package gosmee

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/exp/slog"
	"gotest.tools/v3/assert"
)

func TestParseVersion(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []int
	}{
		{
			name:     "Simple version",
			input:    "1.2.3",
			expected: []int{1, 2, 3},
		},
		{
			name:     "Version with v prefix",
			input:    "v1.2.3",
			expected: []int{1, 2, 3},
		},
		{
			name:     "Version with two components",
			input:    "1.2",
			expected: []int{1, 2, 0},
		},
		{
			name:     "Version with suffix",
			input:    "1.2.3-beta.1",
			expected: []int{1, 2, 3},
		},
		{
			name:     "Version with more components",
			input:    "1.2.3.4",
			expected: []int{1, 2, 3, 4},
		},
		{
			name:     "Development version",
			input:    "dev",
			expected: []int{0, 0, 0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseVersion(tc.input)
			assert.DeepEqual(t, result[:len(tc.expected)], tc.expected)
		})
	}
}

func TestIsOlderVersion(t *testing.T) {
	testCases := []struct {
		name     string
		v1       []int
		v2       []int
		expected bool
	}{
		{
			name:     "Equal versions",
			v1:       []int{1, 2, 3},
			v2:       []int{1, 2, 3},
			expected: false,
		},
		{
			name:     "Lower major version",
			v1:       []int{1, 2, 3},
			v2:       []int{2, 0, 0},
			expected: true,
		},
		{
			name:     "Higher major version",
			v1:       []int{2, 0, 0},
			v2:       []int{1, 5, 0},
			expected: false,
		},
		{
			name:     "Lower minor version",
			v1:       []int{1, 2, 3},
			v2:       []int{1, 3, 0},
			expected: true,
		},
		{
			name:     "Lower patch version",
			v1:       []int{1, 2, 3},
			v2:       []int{1, 2, 4},
			expected: true,
		},
		{
			name:     "More specific version",
			v1:       []int{1, 2, 3},
			v2:       []int{1, 2, 3, 1},
			expected: true,
		},
		{
			name:     "Less specific version",
			v1:       []int{1, 2, 3, 1},
			v2:       []int{1, 2, 3},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isOlderVersion(tc.v1, tc.v2)
			assert.Equal(t, result, tc.expected)
		})
	}
}

func TestCheckServerVersion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	decorate := false

	testCases := []struct {
		name               string
		serverVersion      string
		clientVersion      string
		statusCode         int
		expectError        bool
		errorShouldContain string
	}{
		{
			name:          "Matching versions",
			serverVersion: "1.2.3",
			clientVersion: "1.2.3",
			statusCode:    http.StatusOK,
			expectError:   false,
		},
		{
			name:          "Client newer than server",
			serverVersion: "1.2.3",
			clientVersion: "1.3.0",
			statusCode:    http.StatusOK,
			expectError:   false,
		},
		{
			name:               "Client older than server",
			serverVersion:      "1.3.0",
			clientVersion:      "1.2.3",
			statusCode:         http.StatusOK,
			expectError:        true,
			errorShouldContain: "Please upgrade your gosmee client",
		},
		{
			name:          "Development client version",
			serverVersion: "1.3.0",
			clientVersion: "dev",
			statusCode:    http.StatusOK,
			expectError:   false,
		},
		{
			name:          "Development server version",
			serverVersion: "dev",
			clientVersion: "1.2.3",
			statusCode:    http.StatusOK,
			expectError:   false,
		},
		{
			name:               "Server without version endpoint",
			serverVersion:      "",
			clientVersion:      "1.2.3",
			statusCode:         http.StatusNotFound,
			expectError:        true,
			errorShouldContain: "server appears to be too old",
		},
		{
			name:          "Server with error status",
			serverVersion: "",
			clientVersion: "1.2.3",
			statusCode:    http.StatusInternalServerError,
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.serverVersion != "" {
					w.Header().Set("X-Gosmee-Version", tc.serverVersion)
				}
				w.WriteHeader(tc.statusCode)
				if tc.statusCode == http.StatusOK {
					_, _ = w.Write([]byte(`{"version":"` + tc.serverVersion + `"}`))
				}
			}))
			defer server.Close()

			// Call the function
			err := checkServerVersion(server.URL, tc.clientVersion, logger, decorate)

			if tc.expectError {
				assert.Assert(t, err != nil, "Expected an error but got nil")
				if tc.errorShouldContain != "" {
					assert.Assert(t, strings.Contains(err.Error(), tc.errorShouldContain),
						"Error should contain: %s, but got: %s", tc.errorShouldContain, err.Error())
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
