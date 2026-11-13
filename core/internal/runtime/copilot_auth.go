package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const copilotClientID = "Iv1.b507a08c87ecfe98"
const copilotProvider = "github-copilot"

var copilotAuthHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type copilotDevice struct {
	Code      string `json:"device_code"`
	UserCode  string `json:"user_code"`
	URI       string `json:"verification_uri"`
	Interval  int    `json:"interval"`
	ExpiresIn int    `json:"expires_in"`
}

type copilotSignIn struct {
	code     string
	expires  time.Time
	nextPoll time.Time
	interval time.Duration
}

type CopilotSignInInfo struct {
	UserCode  string `json:"userCode"`
	Interval  int    `json:"interval"`
	ExpiresIn int    `json:"expiresIn"`
}

func (r *AgentRuntime) CancelCopilotSignIn() {
	r.copilotMu.Lock()
	defer r.copilotMu.Unlock()
	r.copilotLogin = copilotSignIn{}
}

func copilotPost(ctx context.Context, endpoint string, payload any, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := copilotAuthHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub sign-in request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub sign-in failed (status %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(result); err != nil {
		return fmt.Errorf("invalid GitHub sign-in response")
	}
	return nil
}

func discoverCopilotEndpoint(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/copilot_internal/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.95.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	resp, err := copilotAuthHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub Copilot endpoint discovery failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub Copilot endpoint discovery failed (status %d)", resp.StatusCode)
	}
	var result struct {
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid GitHub Copilot endpoint response")
	}
	u, err := url.Parse(result.Endpoints.API)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("GitHub returned an invalid Copilot endpoint")
	}
	host := strings.ToLower(u.Hostname())
	if host != "githubcopilot.com" && !strings.HasSuffix(host, ".githubcopilot.com") {
		return "", fmt.Errorf("GitHub returned an untrusted Copilot endpoint")
	}
	return "https://" + host, nil
}

func (r *AgentRuntime) saveCopilotConnection(token, endpoint string) error {
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return err
	}
	for i := range file.Providers {
		if file.Providers[i].Name == copilotProvider {
			file.Providers[i].ApiURL = endpoint
			if err := r.store.Keys.Set(copilotProvider, token); err != nil {
				return err
			}
			if err := r.store.SaveProviders(file); err != nil {
				return err
			}
			r.evictProviderCache()
			return nil
		}
	}
	return fmt.Errorf("GitHub Copilot provider is missing")
}

func (r *AgentRuntime) StartCopilotSignIn() (CopilotSignInInfo, error) {
	var device copilotDevice
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := copilotPost(ctx, "https://github.com/login/device/code", map[string]string{"client_id": copilotClientID, "scope": "read:user"}, &device); err != nil {
		return CopilotSignInInfo{}, err
	}
	if device.Code == "" || device.UserCode == "" || device.URI != "https://github.com/login/device" || device.ExpiresIn <= 0 {
		return CopilotSignInInfo{}, fmt.Errorf("invalid GitHub device authorization")
	}
	if device.Interval < 5 {
		device.Interval = 5
	}
	r.copilotMu.Lock()
	r.copilotLogin = copilotSignIn{code: device.Code, expires: time.Now().Add(time.Duration(device.ExpiresIn) * time.Second), interval: time.Duration(device.Interval) * time.Second, nextPoll: time.Now().Add(time.Duration(device.Interval) * time.Second)}
	r.copilotMu.Unlock()
	return CopilotSignInInfo{UserCode: device.UserCode, Interval: device.Interval, ExpiresIn: device.ExpiresIn}, nil
}

func (r *AgentRuntime) PollCopilotSignIn() (string, error) {
	r.copilotMu.Lock()
	defer r.copilotMu.Unlock()
	login := &r.copilotLogin
	if login.code == "" || time.Now().After(login.expires) {
		*login = copilotSignIn{}
		return "", fmt.Errorf("GitHub sign-in expired; try again")
	}
	if time.Now().Before(login.nextPoll) {
		return "pending", nil
	}
	login.nextPoll = time.Now().Add(login.interval)
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := copilotPost(ctx, "https://github.com/login/oauth/access_token", map[string]string{"client_id": copilotClientID, "device_code": login.code, "grant_type": "urn:ietf:params:oauth:grant-type:device_code"}, &result)
	if err != nil {
		return "", err
	}
	switch result.Error {
	case "authorization_pending":
		return "pending", nil
	case "slow_down":
		login.interval += 5 * time.Second
		login.nextPoll = time.Now().Add(login.interval)
		return "pending", nil
	case "":
		if result.AccessToken == "" {
			return "", fmt.Errorf("GitHub returned no access token")
		}
		endpoint, err := discoverCopilotEndpoint(ctx, result.AccessToken)
		if err != nil {
			*login = copilotSignIn{}
			return "", err
		}
		if err := r.saveCopilotConnection(result.AccessToken, endpoint); err != nil {
			return "", err
		}
		*login = copilotSignIn{}
		return "connected", nil
	default:
		*login = copilotSignIn{}
		return "", fmt.Errorf("GitHub sign-in failed: %s", result.Error)
	}
}
