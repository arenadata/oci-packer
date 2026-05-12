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

package client

import (
	"context"
	"io"
	"net/http"

	"github.com/arenadata/oci-packer/internal/version"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
)

type Client struct {
	client *http.Client
	authz  docker.Authorizer
}

func New(opts ...docker.AuthorizerOpt) *Client {
	return &Client{
		client: &http.Client{
			Transport: docker.DefaultHTTPTransport(nil),
		},
		authz: docker.NewDockerAuthorizer(opts...),
	}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	if err := c.authz.Authorize(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return resp, nil
	case http.StatusUnauthorized:
		_ = resp.Body.Close()
		if err = c.authz.AddResponses(ctx, []*http.Response{resp}); err != nil {
			return nil, err
		}
		if err = c.authz.Authorize(ctx, req); err != nil {
			return nil, err
		}

		return c.client.Do(req)
	default:
		err = remoteerror.NewUnexpectedStatusErr(resp)
		_ = resp.Body.Close()
		return nil, err
	}
}

func NewRequest(ctx context.Context, method string, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", version.UserAgent())

	return req, nil
}
