package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"arlo-term/internal/paths"
)

const authBase = "https://ocapi-app.arlo.com"
const apiBase = "https://myapi.arlo.com"
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/88.0.4324.96 Safari/537.36"

type identity struct {
	Token    string                    `json:"token,omitempty"`
	Email    string                    `json:"email"`
	Password string                    `json:"password,omitempty"`
	DeviceID string                    `json:"deviceId"`
	Cookies  map[string][]*http.Cookie `json:"cookies"`
}

type loginClient struct {
	client  *http.Client
	base    string
	headers map[string]string
}

type authData struct {
	Token           string          `json:"token"`
	UserID          string          `json:"userId"`
	MFAState        string          `json:"MFA_State"`
	Issued          json.RawMessage `json:"issued"`
	FactorID        string          `json:"factorId"`
	FactorAuthCode  string          `json:"factorAuthCode"`
	BrowserAuthCode string          `json:"browserAuthCode"`
	AccessToken     struct {
		Token string `json:"token"`
	} `json:"accessToken"`
	Items []struct{ FactorID, FactorType, FactorRole string } `json:"items"`
}

type authError struct {
	Route                   string
	Status, Code, ErrorCode int
}

func (e *authError) Error() string {
	return fmt.Sprintf("Arlo %s failed (HTTP %d, code %d, error %d)", e.Route, e.Status, e.Code, e.ErrorCode)
}

func (c *loginClient) waitForPush(ctx context.Context, challenge string, interval time.Duration) (authData, error) {
	for {
		select {
		case <-ctx.Done():
			return authData{}, fmt.Errorf("Arlo app approval timed out or was canceled: %w", ctx.Err())
		default:
		}
		finish, err := c.callContext(ctx, "POST", "/api/finishAuth", map[string]any{"factorAuthCode": challenge, "isBrowserTrusted": true})
		if err == nil {
			if finish.Token == "" {
				return authData{}, errors.New("Arlo app approval returned no access token")
			}
			return finish, nil
		}
		var apiErr *authError
		if !errors.As(err, &apiErr) || apiErr.Status != 200 || apiErr.Code != 400 || apiErr.ErrorCode != 9233 {
			return authData{}, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return authData{}, fmt.Errorf("Arlo app approval timed out or was canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *loginClient) call(method, route string, body any) (authData, error) {
	return c.callContext(context.Background(), method, route, body)
}

func (c *loginClient) callContext(ctx context.Context, method, route string, body any) (authData, error) {
	var data authData
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return data, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+route, &b)
	if err != nil {
		return data, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return data, fmt.Errorf("Arlo authentication request: %w", err)
	}
	defer res.Body.Close()
	var envelope struct {
		Meta struct {
			Code      int `json:"code"`
			ErrorCode int `json:"error"`
		} `json:"meta"`
		Data authData `json:"data"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope)
	if res.StatusCode != http.StatusOK || err != nil || envelope.Meta.Code != 200 {
		return data, &authError{route, res.StatusCode, envelope.Meta.Code, envelope.Meta.ErrorCode}
	}
	return envelope.Data, nil
}

type Interaction struct {
	Input  func(context.Context, string, string, bool) (string, error)
	Status func(string)
}

var ErrSignedOut = errors.New("not signed in")

func authPath() (string, error) {
	return paths.File("auth.json")
}

func Restore(ctx context.Context) (*Session, error) {
	path, err := authPath()
	if err != nil {
		return nil, err
	}
	return restoreAt(ctx, path)
}

func Login(ctx context.Context, interaction Interaction) (*Session, string, error) {
	path, err := authPath()
	if err != nil {
		return nil, "", err
	}
	return loginAt(ctx, interaction, path)
}

func loadIdentity(path string) (identity, error) {
	var id identity
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return id, nil
	}
	if err != nil {
		return id, err
	}
	if err := json.Unmarshal(b, &id); err != nil {
		return id, fmt.Errorf("read auth.json: %w", err)
	}
	return id, nil
}

func clientFor(id identity) (*http.Client, *cookiejar.Jar) {
	jar, _ := cookiejar.New(nil)
	for host, cookies := range id.Cookies {
		if host == authBase || host == apiBase {
			u, _ := url.Parse(host)
			jar.SetCookies(u, cookies)
		}
	}
	return &http.Client{Jar: jar, Timeout: 30 * time.Second}, jar
}

func restoreAt(ctx context.Context, path string) (*Session, error) {
	id, err := loadIdentity(path)
	if err != nil {
		return nil, err
	}
	if id.Token == "" {
		return nil, ErrSignedOut
	}
	client, _ := clientFor(id)
	s := newSession(client, id.Token)
	s.path = path
	if err := s.request(ctx, "GET", "/hmsweb/users/session/v3", nil, nil, nil); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			return nil, ErrSignedOut
		}
		return nil, err
	}
	return s, nil
}

var silent = Interaction{
	Input:  func(context.Context, string, string, bool) (string, error) { return "", ErrSignedOut },
	Status: func(string) {},
}

func renew(ctx context.Context, path string) (string, error) {
	id, err := loadIdentity(path)
	if err != nil {
		return "", err
	}
	if id.Password == "" {
		return "", ErrSignedOut
	}
	s, err := signIn(ctx, silent, path, id)
	var authErr *authError
	if errors.As(err, &authErr) {
		return "", ErrSignedOut
	}
	if err != nil {
		return "", err
	}
	return s.token(), nil
}

func loginAt(ctx context.Context, interaction Interaction, path string) (*Session, string, error) {
	id, err := loadIdentity(path)
	if err != nil {
		return nil, "", err
	}
	email := strings.TrimSpace(os.Getenv("ARLO_EMAIL"))
	if email == "" {
		email, err = interaction.Input(ctx, "email", "Email", false)
		if err != nil {
			return nil, "", err
		}
	}
	if email != id.Email {
		id = identity{Email: email}
	}
	id.Password = os.Getenv("ARLO_PASSWORD")
	if id.Password == "" {
		id.Password, err = interaction.Input(ctx, "password", "Password", true)
		if err != nil {
			return nil, "", err
		}
	}
	s, err := signIn(ctx, interaction, path, id)
	if err != nil {
		return nil, "", err
	}
	return s, email, nil
}

func signIn(ctx context.Context, interaction Interaction, path string, id identity) (*Session, error) {
	if id.DeviceID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		id.DeviceID = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	client, jar := clientFor(id)
	c := &loginClient{client: client, base: authBase, headers: map[string]string{
		"Auth-Version": "2", "Content-Type": "application/json; charset=UTF-8", "User-Agent": userAgent,
		"Origin": "https://my.arlo.com", "Referer": "https://my.arlo.com/", "Source": "arloCamWeb",
		"schemaVersion": "1", "DNT": "1", "x-user-device-id": id.DeviceID,
		"x-user-device-automation-name": "QlJPV1NFUg==", "x-user-device-type": "BROWSER", "X-Service-Version": "3",
	}}
	interaction.Status("signing in")
	auth, err := c.callContext(ctx, "POST", "/api/auth", map[string]string{"email": id.Email, "password": base64.StdEncoding.EncodeToString([]byte(id.Password)), "language": "en", "EnvSource": "prod"})
	if err != nil {
		return nil, err
	}
	if auth.Token == "" || auth.UserID == "" {
		return nil, errors.New("Arlo returned incomplete authentication data")
	}
	token := auth.Token
	if auth.MFAState != "DISABLED" {
		c.headers["Authorization"] = base64.StdEncoding.EncodeToString([]byte(token))
		browser, browserErr := c.callContext(ctx, "POST", "/api/getFactorId", map[string]string{"factorData": "", "factorType": "BROWSER", "userId": auth.UserID})
		factorID := browser.FactorID
		trusted := browserErr == nil && factorID != ""
		push := false
		if !trusted {
			issued := string(auth.Issued)
			if len(issued) > 0 && issued[0] == '"' {
				_ = json.Unmarshal(auth.Issued, &issued)
			}
			factors, e := c.callContext(ctx, "GET", "/api/getFactors?data="+url.QueryEscape(issued), nil)
			if e != nil {
				return nil, e
			}
			for _, f := range factors.Items {
				if f.FactorRole == "PRIMARY" && (f.FactorType == "EMAIL" || f.FactorType == "SMS" || f.FactorType == "PUSH") {
					factorID = f.FactorID
					push = f.FactorType == "PUSH"
					break
				}
			}
			if factorID == "" {
				return nil, errors.New("Arlo has no supported primary 2FA method (app approval, Email or SMS)")
			}
		}
		startBody := map[string]string{"factorId": factorID, "factorType": "BROWSER", "userId": auth.UserID}
		if push {
			startBody = map[string]string{"factorType": "", "userId": auth.UserID}
		}
		start, e := c.callContext(ctx, "POST", "/api/startAuth", startBody)
		if e != nil {
			return nil, e
		}
		browserCode := ""
		if trusted {
			token = start.AccessToken.Token
		} else {
			if start.FactorAuthCode == "" {
				return nil, errors.New("Arlo did not return a 2FA challenge")
			}
			finish, e := c.verify(ctx, interaction, push, start.FactorAuthCode, startBody)
			if e != nil {
				return nil, e
			}
			token = finish.Token
			browserCode = finish.BrowserAuthCode
			if browserCode == "" {
				return nil, errors.New("Arlo did not return a pairing code")
			}
		}
		if token == "" {
			return nil, errors.New("Arlo did not return an access token")
		}
		c.headers["Authorization"] = base64.StdEncoding.EncodeToString([]byte(token))
		if _, err = c.callContext(ctx, "GET", fmt.Sprintf("/api/validateAccessToken?data=%d", time.Now().Unix()), nil); err != nil {
			return nil, err
		}
		if !trusted {
			if _, err = c.callContext(ctx, "POST", "/api/startPairingFactor", map[string]string{"factorAuthCode": browserCode, "factorData": "", "factorType": "BROWSER"}); err != nil {
				return nil, err
			}
		}
	}
	s := newSession(client, token)
	if err = s.request(ctx, "GET", "/hmsweb/users/session/v3", nil, nil, nil); err != nil {
		return nil, err
	}
	s.path = path
	id.Token = token
	id.Cookies = map[string][]*http.Cookie{}
	for _, host := range []string{authBase, apiBase} {
		u, _ := url.Parse(host)
		id.Cookies[host] = jar.Cookies(u)
	}
	if err = saveIdentity(path, id); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *loginClient) verify(ctx context.Context, interaction Interaction, push bool, challenge string, startBody map[string]string) (authData, error) {
	for {
		stepCtx, cancel := context.WithCancel(ctx)
		kind, label := "code", "Code"
		if push {
			kind, label = "push", "Approve the sign-in in the Arlo app on your phone"
		}
		type result struct {
			data authData
			err  error
		}
		results := make(chan result, 1)
		if push {
			go func(code string) {
				pollCtx, stop := context.WithTimeout(stepCtx, 2*time.Minute)
				defer stop()
				data, err := c.waitForPush(pollCtx, code, 5*time.Second)
				results <- result{data, err}
			}(challenge)
		}
		type answer struct {
			value string
			err   error
		}
		answers := make(chan answer, 1)
		go func() { value, err := interaction.Input(stepCtx, kind, label, false); answers <- answer{value, err} }()
		var action answer
		select {
		case <-ctx.Done():
			cancel()
			return authData{}, ctx.Err()
		case r := <-results:
			cancel()
			if r.err == nil {
				return r.data, nil
			}
			interaction.Status("approval failed or expired")
			value, err := interaction.Input(ctx, "resend", "Approval failed or expired", false)
			action = answer{value, err}
		case action = <-answers:
			cancel()
			if push {
				<-results
			}
		}
		if action.err != nil {
			return authData{}, action.err
		}
		if action.value == "resend" {
			interaction.Status("sending a new verification")
			start, err := c.callContext(ctx, "POST", "/api/startAuth", startBody)
			if err != nil {
				return authData{}, err
			}
			if start.FactorAuthCode == "" {
				return authData{}, errors.New("Arlo did not return a verification challenge")
			}
			challenge = start.FactorAuthCode
			continue
		}
		if !push {
			finish, err := c.callContext(ctx, "POST", "/api/finishAuth", map[string]any{"factorAuthCode": challenge, "otp": action.value, "isBrowserTrusted": true})
			if err == nil {
				return finish, nil
			}
			interaction.Status("verification failed, try again")
		}
	}
}

func newSession(client *http.Client, token string) *Session {
	return &Session{apiBase: apiBase, client: client, headers: map[string]string{"Auth-Version": "2", "Authorization": token, "User-Agent": userAgent}}
}

func saveIdentity(path string, id identity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(id)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "auth-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
