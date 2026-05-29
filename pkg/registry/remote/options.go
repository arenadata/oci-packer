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

package remote

import "github.com/arenadata/oci-packer/pkg/http"

type Option func(*Client)

func WithPlainHttp() Option {
	return func(c *Client) {
		c.plainHttp = true
	}
}

func WithInsecure() Option {
	return func(c *Client) {
		c.insecure = true
	}
}

func WithCreds(login, password string) Option {
	return func(c *Client) {
		c.login = login
		c.password = password
	}
}

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}
