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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/client"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	remoteerror "github.com/containerd/containerd/v2/core/remotes/errors"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var resolveHeaders = []string{
	images.MediaTypeDockerSchema2Manifest,
	images.MediaTypeDockerSchema2ManifestList,
	ocispec.MediaTypeImageManifest,
	ocispec.MediaTypeImageIndex,
	"*/*",
}

type registryResolver struct {
	ref    reference.Reference
	client *client.Client

	plainHttp bool
}

func New(ref string, plainHttp bool) (registry.Resolver, error) {
	parsed, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	return &registryResolver{ref: parsed, client: client.New(), plainHttp: plainHttp}, nil
}

func parseReference(repoRef reference.Reference, ref string) (reference.Reference, error) {
	if len(ref) > 0 && strings.ContainsAny(ref, "@:") {
		// [registry.host/[repo/]]image[:tag|@digest]
		var err error
		var parsedReference reference.Reference
		if strings.HasPrefix(ref, repoRef.Host) {
			parsedReference, err = reference.Parse(ref)
		} else {
			parsedReference, err = reference.ParseImage(ref)
		}
		if err != nil {
			return reference.Reference{}, err
		}

		repoRef.Image = parsedReference.Image
		repoRef.Ref = parsedReference.Ref
		return repoRef, nil
	}

	if len(ref) > 0 {
		// tag or digest
		repoRef.Ref = ref
	}

	return repoRef, nil
}

func (r registryResolver) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	repoRef, err := parseReference(r.ref, ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	return r.resolve(ctx, repoRef)
}

func (r registryResolver) resolve(ctx context.Context, repoRef reference.Reference) (ocispec.Descriptor, error) {
	repoUrl := fromRef(repoRef, r.plainHttp)
	resp, err := r.head(ctx, repoUrl.manifests(), resolveHeaders...)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	return ocispec.Descriptor{
		MediaType: getManifestMediaType(resp),
		Digest:    digest.Digest(resp.Header.Get("Docker-Content-Digest")),
		Size:      resp.ContentLength,
	}, nil
}

func getManifestMediaType(resp *http.Response) string {
	// Strip encoding data (manifests should always be ascii JSON)
	contentType := resp.Header.Get("Content-Type")
	if sp := strings.IndexByte(contentType, ';'); sp != -1 {
		contentType = contentType[0:sp]
	}

	// As of Apr 30 2019 the registry.access.redhat.com registry does not specify
	// the content type of any data but uses schema1 manifests.
	if contentType == "text/plain" {
		contentType = images.MediaTypeDockerSchema1Manifest
	}
	return contentType
}

func (r registryResolver) Exists(ctx context.Context, ref string) (bool, error) {
	repoRef, err := parseReference(r.ref, ref)
	if err != nil {
		return false, err
	}

	repoUrl := fromRef(repoRef, r.plainHttp)

	resp, err := r.head(ctx, repoUrl.manifests(), resolveHeaders...)
	if err != nil {
		if e, ok := errors.AsType[remoteerror.ErrUnexpectedStatus](err); ok && e.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}

	return resp.StatusCode == http.StatusOK, nil
}

func (r registryResolver) head(ctx context.Context, u string, accept ...string) (*http.Response, error) {
	req, err := newHeadRequest(ctx, u)
	if err != nil {
		return nil, err
	}

	if len(accept) > 0 {
		req.Header.Set("Accept", strings.Join(accept, ", "))
	} else {
		req.Header.Set("Accept", "*/*")
	}

	resp, err := r.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return resp, err
}

func (r registryResolver) Mount(ctx context.Context, ref string) error {
	repoRef, err := parseReference(r.ref, ref)
	if err != nil {
		return err
	}

	desc, err := r.resolve(ctx, repoRef)
	if err != nil {
		return err
	}

	return r.mountDescriptor(ctx, desc, repoRef.Image)
}

func (r registryResolver) mountDescriptor(ctx context.Context, desc ocispec.Descriptor, from string) error {
	switch desc.MediaType {
	case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispec.Index
		if err := r.fetch(ctx, desc, &index); err != nil {
			return err
		}
		for _, manifest := range index.Manifests {
			if err := r.mountDescriptor(ctx, manifest, from); err != nil {
				return err
			}
		}
		return nil
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		return r.mountManifest(ctx, desc, from)
	}

	repoRef := r.ref
	repoRef.Image = from
	repoRef.Ref = desc.Digest.String()

	repoUrl := fromRef(repoRef, r.plainHttp)
	req, err := newPostRequest(ctx, repoUrl.mount(desc.Digest, from), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", desc.MediaType)

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return remoteerror.NewUnexpectedStatusErr(resp)
	}

	actual := resp.Header.Get("Docker-Content-Digest")
	if len(actual) > 0 && actual == repoRef.Ref {
		return fmt.Errorf("got digest %s, expected %s", actual, repoRef.Ref)
	}

	return nil
}

func (r registryResolver) mountManifest(ctx context.Context, desc ocispec.Descriptor, from string) error {
	var manifest ocispec.Manifest
	if err := r.fetch(ctx, desc, &manifest); err != nil {
		return err
	}

	if err := r.mountDescriptor(ctx, manifest.Config, from); err != nil {
		return err
	}
	for _, layer := range manifest.Layers {
		if err := r.mountDescriptor(ctx, layer, from); err != nil {
			return err
		}
	}
	if manifest.Subject != nil {
		if err := r.mountDescriptor(ctx, *manifest.Subject, from); err != nil {
			return err
		}
	}
	return nil
}

func (r registryResolver) fetch(ctx context.Context, desc ocispec.Descriptor, v any) error {
	reader, err := r.Fetcher().Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	return json.NewDecoder(reader).Decode(v)
}

func (r registryResolver) Fetcher() registry.Fetcher {
	return &registryFetcher{ref: r.ref, client: r.client, plainHttp: r.plainHttp}
}

func (r registryResolver) Pusher() registry.Pusher {
	return &registryPusher{ref: r.ref, client: r.client, plainHttp: r.plainHttp}
}
