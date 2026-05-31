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
	"io"
	"net/http"
	"testing"
)

func TestErrUnexpectedStatusError(t *testing.T) {
	err := &ErrUnexpectedStatus{
		Status:        "404 Not Found",
		StatusCode:    http.StatusNotFound,
		RequestMethod: "GET",
		RequestURL:    "http://example.com/api/resource",
	}

	expectedMsg := "unexpected status from GET request to http://example.com/api/resource: 404 Not Found"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message: %s, got: %s", expectedMsg, err.Error())
	}
}

func TestNewUnexpectedStatusErr(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		status         string
		body           string
		expectedCode   int
		expectedStatus string
	}{
		{
			name:           "404 Not Found error",
			statusCode:     http.StatusNotFound,
			status:         "404 Not Found",
			body:           "resource not found",
			expectedCode:   http.StatusNotFound,
			expectedStatus: "404 Not Found",
		},
		{
			name:           "500 Internal Server Error",
			statusCode:     http.StatusInternalServerError,
			status:         "500 Internal Server Error",
			body:           "internal error",
			expectedCode:   http.StatusInternalServerError,
			expectedStatus: "500 Internal Server Error",
		},
		{
			name:           "400 Bad Request",
			statusCode:     http.StatusBadRequest,
			status:         "400 Bad Request",
			body:           "bad request body",
			expectedCode:   http.StatusBadRequest,
			expectedStatus: "400 Bad Request",
		},
		{
			name:           "Empty response body",
			statusCode:     http.StatusNotFound,
			status:         "404 Not Found",
			body:           "",
			expectedCode:   http.StatusNotFound,
			expectedStatus: "404 Not Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
			resp := &http.Response{
				Status:     tt.status,
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(bytes.NewBufferString(tt.body)),
				Request:    req,
			}

			err := NewUnexpectedStatusErr(resp)
			if err == nil {
				t.Fatal("Expected error, got nil")
			}

			errUnexpected, ok := err.(*ErrUnexpectedStatus)
			if !ok {
				t.Fatalf("Expected *ErrUnexpectedStatus, got %T", err)
			}

			if errUnexpected.StatusCode != tt.expectedCode {
				t.Errorf("Expected status code %d, got %d", tt.expectedCode, errUnexpected.StatusCode)
			}

			if errUnexpected.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, errUnexpected.Status)
			}

			if string(errUnexpected.Body) != tt.body {
				t.Errorf("Expected body %s, got %s", tt.body, string(errUnexpected.Body))
			}

			if errUnexpected.RequestMethod != http.MethodGet {
				t.Errorf("Expected method %s, got %s", http.MethodGet, errUnexpected.RequestMethod)
			}

			if errUnexpected.RequestURL != "http://example.com/test" {
				t.Errorf("Expected URL http://example.com/test, got %s", errUnexpected.RequestURL)
			}
		})
	}
}

func TestNewUnexpectedStatusErrWithLargeBody(t *testing.T) {
	// Create a body larger than 64KiB
	largeBody := bytes.Repeat([]byte("x"), 70000)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	resp := &http.Response{
		Status:     "500 Internal Server Error",
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(bytes.NewReader(largeBody)),
		Request:    req,
	}

	err := NewUnexpectedStatusErr(resp)
	errUnexpected := err.(*ErrUnexpectedStatus)

	// The body should be limited to 64KiB (65536 bytes)
	if len(errUnexpected.Body) > 65536 {
		t.Errorf("Expected body to be limited to 65536 bytes, got %d", len(errUnexpected.Body))
	}
}

func TestNewUnexpectedStatusErrWithNilBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	resp := &http.Response{
		Status:     "404 Not Found",
		StatusCode: http.StatusNotFound,
		Body:       nil,
		Request:    req,
	}

	err := NewUnexpectedStatusErr(resp)
	errUnexpected := err.(*ErrUnexpectedStatus)

	if errUnexpected.Body != nil {
		t.Errorf("Expected nil body, got %v", errUnexpected.Body)
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedFound bool
	}{
		{
			name: "Not found error",
			err: &ErrUnexpectedStatus{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
			},
			expectedFound: true,
		},
		{
			name: "Other error",
			err: &ErrUnexpectedStatus{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
			},
			expectedFound: false,
		},
		{
			name:          "Non-ErrUnexpectedStatus error",
			err:           NewTestError("some error"),
			expectedFound: false,
		},
		{
			name:          "Nil error",
			err:           nil,
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFound(tt.err)
			if result != tt.expectedFound {
				t.Errorf("Expected %v, got %v", tt.expectedFound, result)
			}
		})
	}
}

// TestError is a helper for testing
type TestError struct {
	msg string
}

func (e TestError) Error() string {
	return e.msg
}

func NewTestError(msg string) error {
	return TestError{msg: msg}
}
