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

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/client"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type registryPusher struct {
	ref    reference.Reference
	client *client.Client

	plainHttp bool
}

func (p registryPusher) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	rc, ok := r.(io.ReadCloser)
	if ok {
		defer func() { _ = rc.Close() }()
	}

	repoUrl := fromRef(p.ref, p.plainHttp)
	req, err := newPostRequest(ctx, repoUrl.uploads(), nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		return remoteerror.NewUnexpectedStatusErr(resp)
	}

	location, err := resp.Location()
	if err != nil {
		return err
	}

	u := repoUrl.uploadsUrl()
	u.Path = location.Path

	val := location.Query()
	val.Set("digest", desc.Digest.String())
	u.RawQuery = val.Encode()

	req, err = newPutRequest(ctx, u.String(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", desc.MediaType)

	respUpload, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = respUpload.Body.Close() }()

	if respUpload.StatusCode != http.StatusCreated {
		return remoteerror.NewUnexpectedStatusErr(respUpload)
	}

	actual := respUpload.Header.Get("Docker-Content-Digest")
	if actual != desc.Digest.String() {
		return fmt.Errorf("got digest %s, expected %s", actual, desc.Digest)
	}
	return nil
}

func (p registryPusher) PushReference(ctx context.Context, desc ocispec.Descriptor, r io.Reader, opts ...registry.PushOption) error {
	rc, ok := r.(io.ReadCloser)
	if ok {
		defer func() { _ = rc.Close() }()
	}

	switch desc.MediaType {
	case ocispec.MediaTypeImageManifest,
		ocispec.MediaTypeImageIndex,
		images.MediaTypeDockerSchema2Manifest,
		images.MediaTypeDockerSchema2ManifestList:
	default:
		return fmt.Errorf("unsupported media type: %s", desc.MediaType)
	}

	var pushOptions registry.PushOptions
	for _, opt := range opts {
		opt(&pushOptions)
	}

	repoRef, err := parseReference(p.ref, pushOptions.Ref)
	if err != nil {
		return err
	}

	repoUrl := fromRef(repoRef, p.plainHttp)
	req, err := newPutRequest(ctx, repoUrl.manifests(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", desc.MediaType)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return remoteerror.NewUnexpectedStatusErr(resp)
	}

	return nil
}
