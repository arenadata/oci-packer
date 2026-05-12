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

package docker

import (
	"context"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Client struct{}

func (c Client) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	//TODO implement me
	panic("implement me")
}

func (c Client) Exists(ctx context.Context, ref string) (bool, error) {
	//TODO implement me
	panic("implement me")
}

func (c Client) Mount(ctx context.Context, ref string) error {
	//TODO implement me
	panic("implement me")
}

func (c Client) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	//TODO implement me
	panic("implement me")
}

func (c Client) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	//TODO implement me
	panic("implement me")
}

func (c Client) SetTag(ctx context.Context, desc ocispec.Descriptor) error {
	//TODO implement me
	panic("implement me")
}
