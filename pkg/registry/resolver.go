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

package registry

import (
	"context"
	"io"

	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// Resolver provides remotes based on a locator.
type Resolver interface {
	Resolve(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, error)
	Exists(ctx context.Context, ref reference.Reference) (bool, error)

	Fetcher
	Pusher
}

// Fetcher fetches content.
type Fetcher interface {
	FetchReference(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, io.ReadCloser, error)
	// Fetch the resource identified by the descriptor.
	Fetch(ctx context.Context, ref reference.Reference) (io.ReadCloser, error)
}

// Pusher pushes content
type Pusher interface {
	MountFrom(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, error)
	Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error
	SetTag(ctx context.Context, desc ocispecv1.Descriptor) error
}
