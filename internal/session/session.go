package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

type Session struct {
	apiBase string
	client  *http.Client
	path    string
	mu      sync.Mutex
	headers map[string]string
}

type APIError struct {
	Status        int
	Method, Route string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s failed (%d)", e.Method, e.Route, e.Status)
}

func (s *Session) Request(method, route string, body any, extra map[string]string, out any) error {
	return s.request(context.Background(), method, route, body, extra, out)
}

func (s *Session) request(ctx context.Context, method, route string, body any, extra map[string]string, out any) error {
	stale := s.token()
	err := s.do(ctx, method, route, body, extra, out)
	var apiErr *APIError
	if s.path == "" || !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		return err
	}
	if err := s.refresh(ctx, stale); err != nil {
		if errors.Is(err, ErrSignedOut) {
			return apiErr
		}
		return err
	}
	return s.do(ctx, method, route, body, extra, out)
}

func (s *Session) token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers["Authorization"]
}

func (s *Session) refresh(ctx context.Context, stale string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headers["Authorization"] != stale {
		return nil
	}
	token, err := renew(ctx, s.path)
	if err != nil {
		return err
	}
	s.headers["Authorization"] = token
	return nil
}

func (s *Session) do(ctx context.Context, method, route string, body any, extra map[string]string, out any) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, s.apiBase+route, bytes.NewReader(b))
	if err != nil {
		return err
	}
	s.mu.Lock()
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	s.mu.Unlock()
	req.Header.Set("content-type", "application/json; charset=UTF-8")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var r struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil || res.StatusCode != http.StatusOK || !r.Success {
		return &APIError{res.StatusCode, method, route}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Data, out)
}
