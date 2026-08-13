package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	googleAuthorizationEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint         = "https://oauth2.googleapis.com/token"
	googleUserInfoEndpoint      = "https://openidconnect.googleapis.com/v1/userinfo"
)

// GoogleConfig contains OAuth credentials from a Google Cloud web client.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

// GoogleClient implements OAuth authorization code flow with PKCE.
type GoogleClient struct {
	clientID, clientSecret, redirectURL string
	httpClient                          *http.Client
}

// NewGoogleClient creates a Google OAuth client. Empty credentials disable it.
func NewGoogleClient(cfg GoogleConfig) *GoogleClient {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &GoogleClient{
		clientID: cfg.ClientID, clientSecret: cfg.ClientSecret,
		redirectURL: cfg.RedirectURL, httpClient: client,
	}
}

func (g *GoogleClient) Enabled() bool {
	return g != nil && g.clientID != "" && g.clientSecret != "" && g.redirectURL != ""
}

func (g *GoogleClient) AuthURL(state, codeChallenge string) string {
	values := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {g.redirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return googleAuthorizationEndpoint + "?" + values.Encode()
}

func (g *GoogleClient) Exchange(ctx context.Context, code, verifier string) (GoogleProfile, error) {
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"redirect_uri":  {g.redirectURL},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return GoogleProfile{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return GoogleProfile{}, fmt.Errorf("exchange Google code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return GoogleProfile{}, fmt.Errorf("exchange Google code: status %d", response.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil || token.AccessToken == "" {
		return GoogleProfile{}, fmt.Errorf("decode Google token response")
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoEndpoint, nil)
	if err != nil {
		return GoogleProfile{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	response, err = g.httpClient.Do(request)
	if err != nil {
		return GoogleProfile{}, fmt.Errorf("load Google user info: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return GoogleProfile{}, fmt.Errorf("load Google user info: status %d", response.StatusCode)
	}
	var profile GoogleProfile
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile); err != nil {
		return GoogleProfile{}, fmt.Errorf("decode Google user info: %w", err)
	}
	return profile, nil
}
