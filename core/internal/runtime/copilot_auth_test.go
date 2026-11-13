package runtime

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

type copilotRoundTrip func(*http.Request) (*http.Response, error)

func (f copilotRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCopilotDeviceSignInStoresToken(t *testing.T) {
	old := copilotAuthHTTP
	t.Cleanup(func() { copilotAuthHTTP = old })
	polls := 0
	copilotAuthHTTP = &http.Client{Transport: copilotRoundTrip(func(r *http.Request) (*http.Response, error) {
		result := `{"device_code":"secret-device","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","interval":5,"expires_in":900}`
		if r.URL.Path == "/copilot_internal/user" {
			result = `{"endpoints":{"api":"https://api.business.githubcopilot.com"}}`
		}
		if strings.HasSuffix(r.URL.Path, "/access_token") {
			polls++
			if polls == 1 {
				result = `{"error":"authorization_pending"}`
			} else {
				result = `{"access_token":"access-secret"}`
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(result)), Header: make(http.Header), Request: r}, nil
	})}
	dir := t.TempDir()
	keys := config.NewMemoryKeyStore()
	rt := WithKeyStore(dir, config.NewConfig(dir), keys)
	info, err := rt.StartCopilotSignIn()
	if err != nil || info.UserCode != "ABCD-1234" {
		t.Fatalf("sign in = %+v, %v", info, err)
	}
	state, err := rt.PollCopilotSignIn()
	if err != nil || state != "pending" || polls != 0 {
		t.Fatalf("early poll = %q, %v, calls %d", state, err, polls)
	}
	rt.copilotLogin.nextPoll = time.Now().Add(-time.Second)
	state, err = rt.PollCopilotSignIn()
	if err != nil || state != "pending" {
		t.Fatalf("pending = %q, %v", state, err)
	}
	rt.copilotLogin.nextPoll = time.Now().Add(-time.Second)
	state, err = rt.PollCopilotSignIn()
	if err != nil || state != "connected" {
		t.Fatalf("connected = %q, %v", state, err)
	}
	token, ok, err := keys.Get(copilotProvider)
	if err != nil || !ok || token != "access-secret" {
		t.Fatalf("token was not saved")
	}
	if rt.copilotLogin.code != "" {
		t.Fatal("device code remains after sign-in")
	}
	file, err := rt.store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range file.Providers {
		if p.Name == copilotProvider && p.ApiURL == "https://api.business.githubcopilot.com" {
			return
		}
	}
	t.Fatal("Copilot business endpoint not saved")
}

func TestCopilotEndpointRejectsUntrustedHost(t *testing.T) {
	old := copilotAuthHTTP
	t.Cleanup(func() { copilotAuthHTTP = old })
	copilotAuthHTTP = &http.Client{Transport: copilotRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"endpoints":{"api":"https://githubcopilot.com.attacker.invalid"}}`)), Header: make(http.Header), Request: r}, nil
	})}
	if _, err := discoverCopilotEndpoint(context.Background(), "access-secret"); err == nil {
		t.Fatal("accepted an untrusted endpoint")
	}
}
