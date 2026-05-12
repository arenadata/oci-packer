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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Resolver provides remotes based on a locator.
type Resolver interface {
	// Resolve attempts to resolve the reference into a name and descriptor.
	//
	// The argument `ref` should be a scheme-less URI representing the remote.
	// Structurally, it has a host and path. The "host" can be used to directly
	// reference a specific host or be matched against a specific handler.
	//
	// The returned name should be used to identify the referenced entity.
	// Depending on the remote namespace, this may be immutable or mutable.
	// While the name may differ from ref, it should itself be a valid ref.
	//
	// If the resolution fails, an error will be returned.
	Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error)

	Exists(ctx context.Context, ref string) (bool, error)

	Mount(ctx context.Context, ref string) error

	// Fetcher returns a new fetcher for the provided reference.
	// All content fetched from the returned fetcher will be
	// from the namespace referred to by ref.
	Fetcher() Fetcher

	// Pusher returns a new pusher for the provided reference
	// The returned Pusher should satisfy content.Ingester and concurrent attempts
	// to push the same blob using the Ingester API should result in ErrUnavailable.
	Pusher() Pusher
}

// Fetcher fetches content.
// A fetcher implementation may implement the FetcherByDigest interface too.
type Fetcher interface {
	// Fetch the resource identified by the descriptor.
	Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error)
}

// Pusher pushes content
type Pusher interface {
	// Push returns a content writer for the given resource identified
	// by the descriptor.
	Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error
	PushReference(ctx context.Context, desc ocispec.Descriptor, r io.Reader, opts ...PushOption) error
}

type PushOption func(*PushOptions)

type PushOptions struct {
	Ref string
}
