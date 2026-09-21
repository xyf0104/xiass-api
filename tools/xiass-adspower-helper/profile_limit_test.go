package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdsPowerProfileCapacityErrorOnlyForProfileCreation(t *testing.T) {
	for _, tc := range []struct {
		path, message string
		capacity      bool
	}{
		{"/api/v2/browser-profile/create", "If the number of imported accounts exceeds the limit of 52, please delete some accounts and try again.", true},
		{"/api/v2/browser-profile/create", "If the number of imported accounts exceeds the limit of 10, please delete some accounts and try again.", true},
		{"/api/v2/browser-profile/create", "API rate limit exceeded", false},
		{"/api/v2/browser-profile/start", "If the number of imported accounts exceeds the limit of 52", false},
	} {
		t.Run(tc.path+tc.message, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(adsPowerEnvelope{Code: -1, Msg: tc.message})
			}))
			defer server.Close()
			client := newAdsPowerClient(&config{AdsPowerBaseURL: server.URL})
			err := client.doOnce(context.Background(), http.MethodPost, tc.path, map[string]string{}, nil)
			require.Error(t, err)
			require.Equal(t, tc.capacity, errors.Is(err, errAdsPowerProfileLimit))
		})
	}
}
