package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthChallengeAndCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "encoded-token" {
			t.Error("missing authentication header")
		}
		switch r.URL.Path {
		case "/api/startAuth":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["factorId"] != "email-factor" || body["factorType"] != "BROWSER" {
				t.Error("incorrect challenge request")
			}
			http.SetCookie(w, &http.Cookie{Name: "trust", Value: "saved", Path: "/"})
			w.Write([]byte(`{"meta":{"code":200},"data":{"factorAuthCode":"challenge"}}`))
		case "/api/finishAuth":
			cookie, err := r.Cookie("trust")
			if err != nil || cookie.Value != "saved" {
				t.Error("trust cookie was not preserved")
			}
			w.Write([]byte(`{"meta":{"code":200},"data":{"token":"access","browserAuthCode":"pair"}}`))
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &loginClient{client: &http.Client{Jar: jar}, base: server.URL, headers: map[string]string{"Authorization": "encoded-token"}}
	start, err := client.call("POST", "/api/startAuth", map[string]string{"factorId": "email-factor", "factorType": "BROWSER"})
	if err != nil || start.FactorAuthCode != "challenge" {
		t.Fatalf("challenge: %+v, %v", start, err)
	}
	finish, err := client.call("POST", "/api/finishAuth", map[string]string{"factorAuthCode": start.FactorAuthCode, "otp": "123456"})
	if err != nil || finish.Token != "access" || finish.BrowserAuthCode != "pair" {
		t.Fatalf("finish: %+v, %v", finish, err)
	}
}

func TestAuthErrorsDoNotExposeResponse(t *testing.T) {
	for _, body := range []string{`{"meta":{"code":401},"data":{"token":"secret"}}`, `<html>secret</html>`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			client := &loginClient{client: server.Client(), base: server.URL}
			_, err := client.call("POST", "/api/auth", nil)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}

func TestAPISessionUsesRawToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "raw-token" || r.Header.Get("Auth-Version") != "2" {
			t.Error("incorrect API authorization")
		}
		w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
	}))
	defer server.Close()
	s := &Session{apiBase: server.URL, client: server.Client(), headers: map[string]string{"Authorization": "raw-token", "Auth-Version": "2"}}
	var data struct{ OK bool }
	if err := s.Request("GET", "/hmsweb/users/session/v3", nil, nil, &data); err != nil || !data.OK {
		t.Fatalf("session: %+v, %v", data, err)
	}
}

func TestPushApproval(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["otp"]; ok {
			t.Error("push must not send an OTP")
		}
		if body["factorAuthCode"] != "push-challenge" || body["isBrowserTrusted"] != true {
			t.Error("incorrect push payload")
		}
		if attempts < 3 {
			w.Write([]byte(`{"meta":{"code":400,"error":9233}}`))
			return
		}
		w.Write([]byte(`{"meta":{"code":200},"data":{"token":"approved","browserAuthCode":"trust-code"}}`))
	}))
	defer server.Close()
	c := &loginClient{client: server.Client(), base: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := c.waitForPush(ctx, "push-challenge", time.Millisecond)
	if err != nil || result.Token != "approved" || result.BrowserAuthCode != "trust-code" || attempts != 3 {
		t.Fatalf("approval failed: attempts=%d err=%v", attempts, err)
	}
}

func TestPushRejectedOrExpired(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		timeout    bool
	}{
		{"rejected", `{"meta":{"code":400,"error":9999}}`, false},
		{"expired", `{"meta":{"code":400,"error":9233}}`, true},
		{"missing-token", `{"meta":{"code":200},"data":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts++; w.Write([]byte(tc.body)) }))
			defer server.Close()
			c := &loginClient{client: server.Client(), base: server.URL}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := c.waitForPush(ctx, "challenge", time.Second)
			if err == nil {
				t.Fatal("expected approval failure")
			}
			if tc.timeout && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected timeout: %v", err)
			}
			if attempts != 1 {
				t.Fatalf("unexpected retries: %d", attempts)
			}
		})
	}
}

func TestResendReplacesPushChallenge(t *testing.T) {
	var starts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/startAuth" {
			starts.Add(1)
			w.Write([]byte(`{"meta":{"code":200},"data":{"factorAuthCode":"new-challenge"}}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["factorAuthCode"] == "new-challenge" {
			w.Write([]byte(`{"meta":{"code":200},"data":{"token":"approved","browserAuthCode":"trust"}}`))
			return
		}
		w.Write([]byte(`{"meta":{"code":400,"error":9233}}`))
	}))
	defer server.Close()
	c := &loginClient{client: server.Client(), base: server.URL}
	var inputs atomic.Int32
	interaction := Interaction{Status: func(string) {}, Input: func(ctx context.Context, kind, label string, secret bool) (string, error) {
		if inputs.Add(1) == 1 {
			return "resend", nil
		}
		<-ctx.Done()
		return "", ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := c.verify(ctx, interaction, true, "old-challenge", map[string]string{"factorType": "", "userId": "user"})
	if err != nil || result.Token != "approved" || starts.Load() != 1 {
		t.Fatalf("resend failed: %v, starts=%d", err, starts.Load())
	}
}

func TestSavedIdentityIsPrivateAndContainsNoPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "arlo", "auth.json")
	if err := saveIdentity(path, identity{Email: "user@example.com", Token: "session-token", DeviceID: "device"}); err != nil {
		t.Fatal(err)
	}
	if err := saveIdentity(path, identity{Email: "user@example.com", Token: "replacement-token", DeviceID: "device"}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, filepath.Dir(path)} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("credentials accessible by other users: %v", info.Mode())
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved identity
	if err = json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Token != "replacement-token" || strings.Contains(string(b), "password") {
		t.Fatal("incorrect saved identity")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary credential files left behind")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRestoreReusesValidSessionOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"valid", 200, `{"success":true,"data":{}}`, nil},
		{"expired", 401, `{"success":false}`, ErrSignedOut},
		{"cloudflare", 403, `<html>challenge</html>`, errors.New("blocked")},
		{"server-error", 503, `{"success":false}`, errors.New("server")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "arlo", "auth.json")
			if err := saveIdentity(path, identity{Email: "saved@example.com", Token: "cached-token", DeviceID: "device"}); err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultTransport
			defer func() { http.DefaultTransport = previous }()
			http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/hmsweb/users/session/v3" || r.Header.Get("Authorization") != "cached-token" {
					t.Error("cached session was not validated")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			session, err := restoreAt(context.Background(), path)
			switch {
			case tc.want == nil && (err != nil || session == nil):
				t.Fatalf("session reuse failed: %v", err)
			case tc.want == ErrSignedOut && !errors.Is(err, ErrSignedOut):
				t.Fatalf("expected signed out: %v", err)
			case tc.want != nil && tc.want != ErrSignedOut && (err == nil || errors.Is(err, ErrSignedOut)):
				t.Fatalf("server failure should not look signed out: %v", err)
			}
		})
	}
}

func TestRestoreWithoutAuthIsSignedOut(t *testing.T) {
	if _, err := restoreAt(context.Background(), filepath.Join(t.TempDir(), "auth.json")); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("expected signed out: %v", err)
	}
}

func TestLoginAsksForEmailThenPassword(t *testing.T) {
	t.Setenv("ARLO_EMAIL", "")
	t.Setenv("ARLO_PASSWORD", "")
	var asked []string
	stop := errors.New("stop at password")
	interaction := Interaction{Status: func(string) {}, Input: func(ctx context.Context, kind, label string, secret bool) (string, error) {
		asked = append(asked, kind)
		if kind == "password" {
			if !secret {
				t.Error("password must be secret")
			}
			return "", stop
		}
		return "user@example.com", nil
	}}
	_, _, err := loginAt(context.Background(), interaction, filepath.Join(t.TempDir(), "auth.json"))
	if !errors.Is(err, stop) || strings.Join(asked, ",") != "email,password" {
		t.Fatalf("unexpected prompts %v: %v", asked, err)
	}
}
