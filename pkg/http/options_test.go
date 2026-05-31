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
	"context"
	"net/http"
	"testing"
)

func TestWithInsecure(t *testing.T) {
	client := New(WithInsecure())

	if !client.insecure {
		t.Error("Expected insecure option to be set")
	}
}

func TestWithAuthCreds(t *testing.T) {
	expectedUser := "testuser"
	expectedPass := "testpass"

	credsFunc := func(s string) (string, string, error) {
		return expectedUser, expectedPass, nil
	}

	client := New(WithAuthCreds(credsFunc))

	if client.creds == nil {
		t.Fatal("Expected creds function to be set")
	}

	user, pass, err := client.creds("test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if user != expectedUser {
		t.Errorf("Expected user %s, got %s", expectedUser, user)
	}

	if pass != expectedPass {
		t.Errorf("Expected password %s, got %s", expectedPass, pass)
	}
}

func TestWithContentType(t *testing.T) {
	tests := []struct {
		name           string
		contentType    string
		expectedHeader string
	}{
		{
			name:           "JSON content type",
			contentType:    "application/json",
			expectedHeader: "application/json",
		},
		{
			name:           "XML content type",
			contentType:    "application/xml",
			expectedHeader: "application/xml",
		},
		{
			name:           "Plain text content type",
			contentType:    "text/plain",
			expectedHeader: "text/plain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.com", nil)

			opt := WithContentType(tt.contentType)
			opt(req)

			if req.Header.Get("Content-Type") != tt.expectedHeader {
				t.Errorf("Expected Content-Type %s, got %s", tt.expectedHeader, req.Header.Get("Content-Type"))
			}
		})
	}
}

func TestWithAccept(t *testing.T) {
	tests := []struct {
		name           string
		accepts        []string
		expectedHeader string
	}{
		{
			name:           "Single accept type",
			accepts:        []string{"application/json"},
			expectedHeader: "application/json",
		},
		{
			name:           "Multiple accept types",
			accepts:        []string{"application/json", "text/plain", "application/xml"},
			expectedHeader: "application/json,text/plain,application/xml",
		},
		{
			name:           "Accept with wildcard",
			accepts:        []string{"*/*"},
			expectedHeader: "*/*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)

			opt := WithAccept(tt.accepts...)
			opt(req)

			if req.Header.Get("Accept") != tt.expectedHeader {
				t.Errorf("Expected Accept %s, got %s", tt.expectedHeader, req.Header.Get("Accept"))
			}
		})
	}
}

func TestMultipleOptions(t *testing.T) {
	client := New(
		WithInsecure(),
		WithAuthCreds(func(s string) (string, string, error) {
			return "user", "pass", nil
		}),
	)

	if !client.insecure {
		t.Error("Expected insecure option to be set")
	}

	if client.creds == nil {
		t.Error("Expected creds function to be set")
	}
}

func TestWithAcceptEmpty(t *testing.T) {
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)

	opt := WithAccept()
	opt(req)

	if req.Header.Get("Accept") != "" {
		t.Errorf("Expected empty Accept header, got %s", req.Header.Get("Accept"))
	}
}
