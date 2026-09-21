package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const agTokenURL = "https://oauth2.googleapis.com/token"
const agRedirect = "http://localhost:51121/oauth-callback"

var agAuthMu sync.Mutex

// Client configuration stays outside source control and compiled binaries.
// These identify the OAuth application, separately from saved user tokens.
func agOAuthClient() (id, secret string, err error) {
	id = strings.TrimSpace(os.Getenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_ID"))
	secret = strings.TrimSpace(os.Getenv("CVKEHARNESS_ANTIGRAVITY_CLIENT_SECRET"))
	if id == "" || secret == "" {
		return "", "", fmt.Errorf("Antigravity OAuth client is not configured; set CVKEHARNESS_ANTIGRAVITY_CLIENT_ID and CVKEHARNESS_ANTIGRAVITY_CLIENT_SECRET outside source control")
	}
	return id, secret, nil
}

type AntigravityAuth struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ProjectID    string    `json:"project_id"`
}

func AntigravityAuthPath() string {
	if path := strings.TrimSpace(os.Getenv("CVKEHARNESS_ANTIGRAVITY_AUTH")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cvkeharness", "antigravity-auth.json")
}
func randomAGString() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func LoadAntigravityAuth(path string) (AntigravityAuth, error) {
	var auth AntigravityAuth
	f, err := os.Open(path)
	if err != nil {
		return auth, fmt.Errorf("Antigravity login unavailable; run `cvkeharness antigravity login`")
	}
	defer f.Close()
	if err = json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&auth); err != nil {
		return auth, fmt.Errorf("invalid Antigravity credential file; run `cvkeharness antigravity login`")
	}
	if auth.ProjectID == "" || auth.AccessToken == "" {
		return auth, fmt.Errorf("incomplete Antigravity login; run `cvkeharness antigravity login`")
	}
	return auth, nil
}
func saveAntigravityAuth(path string, auth AntigravityAuth) error {
	if path == "" {
		return fmt.Errorf("Antigravity credential path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".antigravity-auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(auth); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func agExchange(ctx context.Context, client *http.Client, endpoint string, values url.Values) (AntigravityAuth, error) {
	clientID, clientSecret, err := agOAuthClient()
	if err != nil {
		return AntigravityAuth{}, err
	}
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return AntigravityAuth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return AntigravityAuth{}, fmt.Errorf("Antigravity OAuth request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return AntigravityAuth{}, fmt.Errorf("Antigravity OAuth failed (HTTP %d); run `cvkeharness antigravity login`", res.StatusCode)
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&token) != nil || token.Access == "" || token.Expires <= 0 {
		return AntigravityAuth{}, fmt.Errorf("invalid Antigravity OAuth response")
	}
	return AntigravityAuth{AccessToken: token.Access, RefreshToken: token.Refresh, ExpiresAt: time.Now().Add(time.Duration(token.Expires) * time.Second)}, nil
}
func antigravityCredential(ctx context.Context, client *http.Client, path string) (AntigravityAuth, error) {
	agAuthMu.Lock()
	defer agAuthMu.Unlock()
	auth, err := LoadAntigravityAuth(path)
	if err != nil {
		return auth, err
	}
	if auth.ExpiresAt.After(time.Now().Add(time.Minute)) {
		return auth, nil
	}
	if auth.RefreshToken == "" {
		return auth, fmt.Errorf("Antigravity login expired; run `cvkeharness antigravity login`")
	}
	fresh, err := agExchange(ctx, client, agTokenURL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {auth.RefreshToken}})
	if err != nil {
		return auth, err
	}
	fresh.ProjectID = auth.ProjectID
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = auth.RefreshToken
	}
	if err = saveAntigravityAuth(path, fresh); err != nil {
		return auth, fmt.Errorf("cannot save refreshed Antigravity login: %w", err)
	}
	return fresh, nil
}

// LoginAntigravity prints an authorization URL; the user completes Google's
// consent screen. Project discovery may complete Google-managed onboarding for
// the default eligible tier when the account has no current tier.
func LoginAntigravity(ctx context.Context, out io.Writer) error {
	clientID, _, err := agOAuthClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:51121")
	if err != nil {
		return fmt.Errorf("cannot listen for Google OAuth on localhost:51121: %w", err)
	}
	verifier, state := randomAGString(), randomAGString()
	challenge := sha256.Sum256([]byte(verifier))
	codes := make(chan string, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/oauth-callback" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid OAuth state", 400)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Google authorization was not completed", 400)
			select {
			case codes <- "":
			default:
			}
			return
		}
		select {
		case codes <- code:
			fmt.Fprint(w, "Authorization received. Return to CvkeHarness to check login completion.")
		default:
			http.Error(w, "Callback already received", 409)
		}
	})
	defer server.Close()
	go server.Serve(listener)
	q := url.Values{"client_id": {clientID}, "redirect_uri": {agRedirect}, "response_type": {"code"}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "scope": {"https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"}}
	fmt.Fprintln(out, "Open this Google authorization URL in your browser:\nhttps://accounts.google.com/o/oauth2/v2/auth?"+q.Encode())
	var code string
	select {
	case code = <-codes:
	case <-ctx.Done():
		return ctx.Err()
	}
	if code == "" {
		return fmt.Errorf("Google authorization declined")
	}
	client := antigravityHTTPClient()
	auth, err := agExchange(ctx, client, agTokenURL, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {agRedirect}})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Google authorization succeeded. Looking up the account's Code Assist project...")
	auth.ProjectID, err = discoverAntigravityProject(ctx, client, auth.AccessToken, agProjectEndpoints, time.Second, out)
	if err != nil {
		return err
	}

	agAuthMu.Lock()
	defer agAuthMu.Unlock()
	if err = saveAntigravityAuth(AntigravityAuthPath(), auth); err != nil {
		return err
	}
	fmt.Fprintln(out, "Antigravity login saved. Select antigravity as your provider and choose a Gemini model.")
	return nil
}
