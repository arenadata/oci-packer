/*
  Copyright (c) 2026 Arenadata Softwer LLC.
  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		opts      []Option
		wantError bool
	}{
		{
			name:      "Create client without options",
			opts:      nil,
			wantError: false,
		},
		{
			name:      "Create client with insecure option",
			opts:      []Option{WithInsecure()},
			wantError: false,
		},
		{
			name: "Create client with auth creds",
			opts: []Option{WithAuthCreds(func(s string) (string, string, error) {
				return "user", "pass", nil
			})},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(tt.opts...)
			if client == nil {
				t.Error("Expected client to be created, got nil")
			}
			if client.client == nil {
				t.Error("Expected http.Client to be initialized")
			}
			if client.authz == nil {
				t.Error("Expected authorizer to be initialized")
			}
		})
	}
}

func TestClientDo(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		expectedError  bool
		expectedStatus int
	}{
		{
			name:           "Successful response with 200",
			statusCode:     http.StatusOK,
			responseBody:   "test response",
			expectedError:  false,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Successful response with 201",
			statusCode:     http.StatusCreated,
			responseBody:   "created",
			expectedError:  false,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Successful response with 202",
			statusCode:     http.StatusAccepted,
			responseBody:   "accepted",
			expectedError:  false,
			expectedStatus: http.StatusAccepted,
		},
		{
			name:           "No content response with 204",
			statusCode:     http.StatusNoContent,
			responseBody:   "",
			expectedError:  false,
			expectedStatus: http.StatusNoContent,
		},
		{
			name:           "Bad request with 400",
			statusCode:     http.StatusBadRequest,
			responseBody:   "bad request",
			expectedError:  true,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Not found with 404",
			statusCode:     http.StatusNotFound,
			responseBody:   "not found",
			expectedError:  true,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client := New()
			ctx := context.Background()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			resp, err := client.Do(req)

			if tt.expectedError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectedError && resp != nil {
				if resp.StatusCode != tt.expectedStatus {
					t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
				}
				_ = resp.Body.Close()
			}
		})
	}
}

func TestClientGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("get response"))
	}))
	defer server.Close()

	client := New()
	ctx := context.Background()
	resp, err := client.Get(ctx, server.URL)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != "get response" {
		t.Errorf("Expected 'get response', got %s", string(body))
	}
}

func TestClientHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New()
	ctx := context.Background()
	resp, err := client.Head(ctx, server.URL)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// HEAD responses must have a non-nil body (per net/http spec Body is
	// always non-nil), but it must be empty — no actual bytes transferred.
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(body) > 0 {
		t.Error("HEAD response body should be empty")
	}
}

func TestClientPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	defer server.Close()

	client := New()
	ctx := context.Background()
	body := bytes.NewReader([]byte("test data"))
	resp, err := client.Post(ctx, server.URL, body)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", resp.StatusCode)
	}

	_ = resp.Body.Close()
}

func TestClientPut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("updated"))
	}))
	defer server.Close()

	client := New()
	ctx := context.Background()
	body := bytes.NewReader([]byte("updated data"))
	resp, err := client.Put(ctx, server.URL, body)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	_ = resp.Body.Close()
}

func TestNewRequest(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		url            string
		expectedError  bool
		expectedMethod string
	}{
		{
			name:           "Create GET request",
			method:         http.MethodGet,
			url:            "http://example.com",
			expectedError:  false,
			expectedMethod: http.MethodGet,
		},
		{
			name:           "Create POST request",
			method:         http.MethodPost,
			url:            "http://example.com",
			expectedError:  false,
			expectedMethod: http.MethodPost,
		},
		{
			name:          "Invalid URL",
			method:        http.MethodGet,
			url:           "://invalid",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			req, err := NewRequest(ctx, tt.method, tt.url, nil)

			if tt.expectedError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectedError && req != nil {
				if req.Method != tt.expectedMethod {
					t.Errorf("Expected method %s, got %s", tt.expectedMethod, req.Method)
				}
				if req.Header.Get("User-Agent") == "" {
					t.Error("Expected User-Agent header to be set")
				}
			}
		})
	}
}

func TestNewRequestWithOptions(t *testing.T) {
	ctx := context.Background()
	req, err := NewRequest(
		ctx,
		http.MethodPost,
		"http://example.com",
		nil,
		WithContentType("application/json"),
		WithAccept("application/json", "text/plain"),
	)

	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %s", req.Header.Get("Content-Type"))
	}

	expectedAccept := "application/json,text/plain"
	if req.Header.Get("Accept") != expectedAccept {
		t.Errorf("Expected Accept: %s, got %s", expectedAccept, req.Header.Get("Accept"))
	}
}
