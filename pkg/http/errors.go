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
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrUnexpectedStatus is returned if a registry API request returned with unexpected HTTP status
type ErrUnexpectedStatus struct {
	Status        string `json:"status"`
	StatusCode    int    `json:"statusCode"`
	Body          []byte `json:"body,omitempty"`
	RequestURL    string `json:"requestURL,omitempty"`
	RequestMethod string `json:"requestMethod,omitempty"`
}

func (e *ErrUnexpectedStatus) Error() string {
	return fmt.Sprintf("unexpected status from %s request to %s: %s", e.RequestMethod, e.RequestURL, e.Status)
}

// NewUnexpectedStatusErr creates an ErrUnexpectedStatus from HTTP response
func NewUnexpectedStatusErr(resp *http.Response) error {
	var b []byte
	if resp.Body != nil {
		b, _ = io.ReadAll(io.LimitReader(resp.Body, 65536)) // 64KiB
	}
	err := &ErrUnexpectedStatus{
		Body:          b,
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		RequestMethod: resp.Request.Method,
	}
	if resp.Request.URL != nil {
		err.RequestURL = resp.Request.URL.String()
	}
	return err
}

func IsNotFound(e error) bool {
	err, ok := errors.AsType[*ErrUnexpectedStatus](e)
	if !ok {
		return false
	}
	return err.StatusCode == http.StatusNotFound
}
