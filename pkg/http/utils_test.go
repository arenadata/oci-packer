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
	"os"
	"path/filepath"
	"testing"
)

func TestResponseToFile(t *testing.T) {
	tests := []struct {
		name            string
		responseBody    string
		contentLength   int64
		expectedError   bool
		checkContent    bool
		expectedContent string
	}{
		{
			name:            "Write response to new file",
			responseBody:    "test content",
			contentLength:   12,
			expectedError:   false,
			checkContent:    true,
			expectedContent: "test content",
		},
		{
			name:            "Empty response body",
			responseBody:    "",
			contentLength:   0,
			expectedError:   false,
			checkContent:    true,
			expectedContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "test_file.txt")

			resp := &http.Response{
				Body:          io.NopCloser(bytes.NewBufferString(tt.responseBody)),
				ContentLength: tt.contentLength,
			}

			err := ResponseToFile(resp, filePath)

			if tt.expectedError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.checkContent && !tt.expectedError {
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}

				if string(content) != tt.expectedContent {
					t.Errorf("Expected content %q, got %q", tt.expectedContent, string(content))
				}
			}
		})
	}
}

func TestResponseToFileExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "existing_file.txt")

	// Create existing file
	originalContent := "original content"
	err := os.WriteFile(filePath, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to write response with same size - should skip
	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewBufferString("new content!")),
		ContentLength: int64(len(originalContent)),
	}

	err = ResponseToFile(resp, filePath)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// File should not be modified
	content, _ := os.ReadFile(filePath)
	if string(content) != originalContent {
		t.Errorf("Expected file to remain unchanged with original content %q, got %q", originalContent, string(content))
	}
}

func TestResponseToFileExistingFileWithDifferentSize(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "existing_file.txt")

	// Create existing file
	originalContent := "original"
	err := os.WriteFile(filePath, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to write response with different size - should overwrite
	newContent := "new content is longer"
	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewBufferString(newContent)),
		ContentLength: int64(len(newContent)),
	}

	err = ResponseToFile(resp, filePath)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// File should be updated
	content, _ := os.ReadFile(filePath)
	if string(content) != newContent {
		t.Errorf("Expected file to be updated with %q, got %q", newContent, string(content))
	}
}

func TestResponseToFileDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewBufferString("test content")),
		ContentLength: 12,
	}

	// Try to write to a directory path
	err := ResponseToFile(resp, tmpDir)

	if err == nil {
		t.Error("Expected error when writing to directory")
	}
}

func TestResponseToFileNestedDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedPath := filepath.Join(tmpDir, "subdir", "nested", "file.txt")

	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewBufferString("nested content")),
		ContentLength: 14,
	}

	err := ResponseToFile(resp, nestedPath)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check if file was created
	content, err := os.ReadFile(nestedPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(content) != "nested content" {
		t.Errorf("Expected 'nested content', got %s", string(content))
	}
}

func TestResponseToFileEmptyExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "empty_file.txt")

	// Create empty existing file
	err := os.WriteFile(filePath, []byte(""), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to write response - should overwrite since file is empty
	newContent := "new content"
	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewBufferString(newContent)),
		ContentLength: int64(len(newContent)),
	}

	err = ResponseToFile(resp, filePath)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// File should be updated
	content, _ := os.ReadFile(filePath)
	if string(content) != newContent {
		t.Errorf("Expected file to be updated with %q, got %q", newContent, string(content))
	}
}

func TestResponseToFileLargeContent(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "large_file.txt")

	// Create large content
	largeContent := bytes.Repeat([]byte("x"), 1000000) // 1MB
	resp := &http.Response{
		Body:          io.NopCloser(bytes.NewReader(largeContent)),
		ContentLength: int64(len(largeContent)),
	}

	err := ResponseToFile(resp, filePath)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if fileInfo.Size() != int64(len(largeContent)) {
		t.Errorf("Expected file size %d, got %d", len(largeContent), fileInfo.Size())
	}
}
