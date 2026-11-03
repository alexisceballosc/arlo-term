package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Session struct {
	apiBase string
	headers map[string]string
	client  *http.Client
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
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.apiBase+route, reader)
	if err != nil {
		return err
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
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
