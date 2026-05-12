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
	"io"
	"net/http"

	"github.com/arenadata/oci-packer/internal/version"

	"github.com/containerd/containerd/v2/core/remotes/docker"
)

type Client struct {
	client *http.Client

	authz docker.Authorizer
	creds func(string) (string, string, error)
}

func New(opts ...Option) *Client {
	cl := &Client{
		client: &http.Client{Transport: docker.DefaultHTTPTransport(nil)},
	}

	for _, opt := range opts {
		opt(cl)
	}

	header := make(http.Header)
	header.Set("User-Agent", version.UserAgent())

	authzOpts := []docker.AuthorizerOpt{
		docker.WithAuthHeader(header),
		docker.WithAuthCreds(cl.creds),
	}

	cl.authz = docker.NewDockerAuthorizer(authzOpts...)

	return cl
}

func (client Client) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if err := client.authz.Authorize(ctx, req); err != nil {
		return nil, err
	}

	resp, err := client.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		if err = client.authz.AddResponses(ctx, []*http.Response{resp}); err != nil {
			return nil, err
		}
		if err = client.authz.Authorize(ctx, req); err != nil {
			return nil, err
		}

		resp, err = client.client.Do(req)
		if err != nil {
			return nil, err
		}
	}

	if req.Method == http.MethodHead || resp.StatusCode == http.StatusNoContent {
		_ = resp.Body.Close()
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return resp, nil
	default:
		defer func() { _ = resp.Body.Close() }()
		err = NewUnexpectedStatusErr(resp)

		return nil, err
	}
}

func (client Client) Get(ctx context.Context, url string, opts ...RequestOption) (*http.Response, error) {
	req, err := NewRequest(ctx, http.MethodGet, url, nil, opts...)
	if err != nil {
		return nil, err
	}

	return client.Do(req)
}

func (client Client) Head(ctx context.Context, url string, opts ...RequestOption) (*http.Response, error) {
	req, err := NewRequest(ctx, http.MethodHead, url, nil, opts...)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func (client Client) Post(ctx context.Context, url string, body io.Reader, opts ...RequestOption) (*http.Response, error) {
	req, err := NewRequest(ctx, http.MethodPost, url, body, opts...)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func (client Client) Put(ctx context.Context, url string, body io.Reader, opts ...RequestOption) (*http.Response, error) {
	req, err := NewRequest(ctx, http.MethodPut, url, body, opts...)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func NewRequest(ctx context.Context, method string, url string, body io.Reader, opts ...RequestOption) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Proto = "HTTP/2.0"
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	req.Header.Set("User-Agent", version.UserAgent())

	for _, opt := range opts {
		opt(req)
	}

	return req, nil
}
