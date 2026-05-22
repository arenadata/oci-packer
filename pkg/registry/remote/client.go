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
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/arenadata/oci-packer/internal/logger"
	packerhttp "github.com/arenadata/oci-packer/pkg/http"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	resolveHeaders = []string{
		images.MediaTypeDockerSchema2Manifest,
		images.MediaTypeDockerSchema2ManifestList,
		ocispecv1.MediaTypeImageManifest,
		ocispecv1.MediaTypeImageIndex,
		"*/*",
	}

	log = logger.New("remote_registry")
)

type Client struct {
	ref    reference.Reference
	client *packerhttp.Client

	plainHttp bool
	insecure  bool
}

func New(ref string, opts ...Option) (registry.Resolver, error) {
	cl := new(Client)
	for _, opt := range opts {
		opt(cl)
	}

	cl.client = packerhttp.New()

	var err error
	cl.ref, err = reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	if cl.ref.Scheme != reference.RegistryScheme {
		return nil, reference.ErrSchemeUnsupported
	}

	return cl, nil
}

func (c Client) urlFromReference(ref string) (registryUrl, error) {
	repoRef, err := reference.ParseRegistryReference(c.ref, ref)
	if err != nil {
		return registryUrl{}, err
	}

	return fromRef(repoRef, c.plainHttp), nil
}

func (c Client) Resolve(ctx context.Context, ref string) (ocispecv1.Descriptor, error) {
	desc, _, err := c.resolve(ctx, ref)
	return desc, err
}

func (c Client) resolve(ctx context.Context, ref string) (ocispecv1.Descriptor, registryUrl, error) {
	repoUrl, err := c.urlFromReference(ref)
	if err != nil {
		return ocispecv1.Descriptor{}, registryUrl{}, err
	}

	resp, err := c.resolveRef(ctx, repoUrl.manifests(), packerhttp.WithAccept(resolveHeaders...))
	if err != nil {
		return ocispecv1.Descriptor{}, registryUrl{}, err
	}

	dgst, err := digest.Parse(resp.Header.Get("Docker-Content-Digest"))
	if err != nil {
		return ocispecv1.Descriptor{}, registryUrl{}, err
	}

	return ocispecv1.Descriptor{
		MediaType: getManifestMediaType(resp),
		Digest:    dgst,
		Size:      resp.ContentLength,
	}, repoUrl, nil
}

func getManifestMediaType(resp *http.Response) string {
	// Strip encoding data (manifests should always be ascii JSON)
	contentType := resp.Header.Get("Content-Type")
	if sp := strings.IndexByte(contentType, ';'); sp > -1 {
		contentType = contentType[:sp]
	}

	// As of Apr 30 2019 the registry.access.redhat.com registry does not specify
	// the content type of any data but uses schema1 manifests.
	if contentType == "text/plain" {
		contentType = images.MediaTypeDockerSchema1Manifest
	}
	return contentType
}

func (c Client) resolveRef(ctx context.Context, url string, opts ...packerhttp.RequestOption) (*http.Response, error) {
	log.WithField("url", url).Debug("resolving reference")
	resp, err := c.client.Head(ctx, url, opts...)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, packerhttp.NewUnexpectedStatusErr(resp)
	}
	if len(resp.Header.Get("Docker-Content-Digest")) == 0 {
		return nil, registry.ErrEmptyDockerContentDigest
	}

	return resp, nil
}

func (c Client) Exists(ctx context.Context, ref string) (bool, error) {
	repoUrl, err := c.urlFromReference(ref)
	if err != nil {
		return false, err
	}

	if _, err = c.resolveRef(ctx, repoUrl.blobs()); err != nil {
		if !packerhttp.IsNotFound(err) {
			log.WithError(err).Error("failed to resolve reference")
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (c Client) Mount(ctx context.Context, ref string) error {
	desc, repoUrl, err := c.resolve(ctx, ref)
	if err != nil {
		return err
	}

	return c.mountDescriptor(ctx, desc, repoUrl.Path)
}

func (c Client) mountDescriptor(ctx context.Context, desc ocispecv1.Descriptor, from string) error {
	log.WithFields(map[string]any{"digest": desc.Digest, "from": from}).Debug("mounting descriptor")

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := c.fetchJson(ctx, desc, &index); err != nil {
			return err
		}
		for _, manifest := range index.Manifests {
			if err := c.mountDescriptor(ctx, manifest, from); err != nil {
				return err
			}
		}
		return nil
	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		return c.mountManifest(ctx, desc, from)
	}

	repoRef := c.ref
	repoRef.Path = from
	repoRef.Ref = desc.Digest.String()

	repoUrl := fromRef(repoRef, c.plainHttp)
	resp, err := c.client.Post(ctx, repoUrl.mount(desc.Digest, from), nil, packerhttp.WithContentType(desc.MediaType))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return packerhttp.NewUnexpectedStatusErr(resp)
	}

	actual := resp.Header.Get("Docker-Content-Digest")
	if len(actual) > 0 && actual != repoRef.Ref {
		return fmt.Errorf("got digest %s, expected %s", actual, repoRef.Ref)
	}

	return nil
}

func (c Client) mountManifest(ctx context.Context, desc ocispecv1.Descriptor, from string) error {
	var manifest ocispecv1.Manifest
	if err := c.fetchJson(ctx, desc, &manifest); err != nil {
		return err
	}

	if err := c.mountDescriptor(ctx, manifest.Config, from); err != nil {
		return err
	}
	for _, layer := range manifest.Layers {
		if err := c.mountDescriptor(ctx, layer, from); err != nil {
			return err
		}
	}
	if manifest.Subject != nil {
		if err := c.mountDescriptor(ctx, *manifest.Subject, from); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) fetchJson(ctx context.Context, desc ocispecv1.Descriptor, v any) error {
	reader, err := c.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	return json.NewDecoder(reader).Decode(v)
}

func (c Client) Fetch(ctx context.Context, desc ocispecv1.Descriptor) (io.ReadCloser, error) {
	repoRef := c.ref
	repoRef.Ref = desc.Digest.String()

	repoUrl := fromRef(repoRef, c.plainHttp)
	resp, err := c.client.Get(ctx, repoUrl.blobs())
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, packerhttp.NewUnexpectedStatusErr(resp)
	}

	return resp.Body, nil
}

func (c Client) Push(ctx context.Context, desc ocispecv1.Descriptor, r io.Reader) error {
	log.WithFields(map[string]any{"digest": desc.Digest, "size": desc.Size}).Debug("pushing blob")

	if ok, err := c.Exists(ctx, desc.Digest.String()); err != nil {
		return err
	} else if ok {
		return registry.ErrAlreadyExists
	}

	repoUrl, err := c.urlFromReference("")
	if err != nil {
		return err
	}

	resp, err := c.client.Post(ctx, repoUrl.uploads(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		return packerhttp.NewUnexpectedStatusErr(resp)
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

	respUpload, err := c.client.Put(ctx, u.String(), r, packerhttp.WithContentType(desc.MediaType))
	if err != nil {
		return err
	}
	defer func() { _ = respUpload.Body.Close() }()

	if respUpload.StatusCode != http.StatusCreated {
		return packerhttp.NewUnexpectedStatusErr(respUpload)
	}

	actual := respUpload.Header.Get("Docker-Content-Digest")
	if actual != desc.Digest.String() {
		return fmt.Errorf("got digest %s, expected %s", actual, desc.Digest)
	}

	return nil
}

func (c Client) SetTag(ctx context.Context, desc ocispecv1.Descriptor) error {
	switch desc.MediaType {
	case ocispecv1.MediaTypeImageManifest,
		ocispecv1.MediaTypeImageIndex,
		images.MediaTypeDockerSchema2Manifest,
		images.MediaTypeDockerSchema2ManifestList:
	default:
		return fmt.Errorf("unsupported media type: %s", desc.MediaType)
	}

	r, err := c.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	repoUrl, _ := c.urlFromReference("")
	log.WithFields(map[string]any{"url": repoUrl.manifests(), "digest": desc.Digest}).Debug("setting tag")

	resp, err := c.client.Put(ctx, repoUrl.manifests(), r, packerhttp.WithContentType(desc.MediaType))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return packerhttp.NewUnexpectedStatusErr(resp)
	}

	return nil
}
