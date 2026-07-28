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

	login    string
	password string
}

func New(ref string, opts ...Option) (registry.Resolver, error) {
	parsedRef, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	return NewRegistryClient(parsedRef, opts...)
}

func NewRegistryClient(ref reference.Reference, opts ...Option) (registry.Resolver, error) {
	if ref.Scheme != reference.RegistryScheme {
		return nil, reference.ErrSchemeUnsupported
	}

	cl := &Client{ref: ref}
	for _, opt := range opts {
		opt(cl)
	}

	if cl.client == nil {
		var clientOpts []packerhttp.Option
		if cl.insecure {
			clientOpts = append(clientOpts, packerhttp.WithInsecure())
		}
		if len(cl.login) > 0 {
			clientOpts = append(clientOpts, packerhttp.WithAuthCreds(func(string) (string, string, error) {
				return cl.login, cl.password, nil
			}))
		}
		cl.client = packerhttp.New(clientOpts...)
	}
	return cl, nil
}

func (c Client) urlFromReference(ref reference.Reference) (reference.Url, error) {
	return c.ref.Merge(ref).URL(c.plainHttp), nil
}

func (c Client) Resolve(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, error) {
	desc, _, err := c.resolve(ctx, ref)
	return desc, err
}

func (c Client) resolve(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, reference.Url, error) {
	repoUrl, err := c.urlFromReference(ref)
	if err != nil {
		return ocispecv1.Descriptor{}, reference.Url{}, err
	}

	resp, err := c.resolveRef(ctx, repoUrl.Manifests(), packerhttp.WithAccept(resolveHeaders...))
	if err != nil {
		return ocispecv1.Descriptor{}, reference.Url{}, err
	}

	dgst, err := digest.Parse(resp.Header.Get("Docker-Content-Digest"))
	if err != nil {
		return ocispecv1.Descriptor{}, reference.Url{}, err
	}

	contentLength := resp.ContentLength
	if contentLength < 0 {
		contentLength = 0
	}

	return ocispecv1.Descriptor{
		MediaType: getManifestMediaType(resp),
		Digest:    dgst,
		Size:      contentLength,
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

func (c Client) Exists(ctx context.Context, ref reference.Reference) (bool, error) {
	repoUrl, err := c.urlFromReference(ref)
	if err != nil {
		return false, err
	}

	if _, err = c.resolveRef(ctx, repoUrl.Blobs()); err != nil {
		if !packerhttp.IsNotFound(err) {
			log.WithError(err).Error("failed to resolve reference")
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (c Client) MountFrom(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, error) {
	desc, repoUrl, err := c.resolve(ctx, ref)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	if err = c.mountDescriptor(ctx, desc, repoUrl.Path()); err != nil {
		return ocispecv1.Descriptor{}, err
	}

	return desc, nil
}

func (c Client) mountDescriptor(ctx context.Context, desc ocispecv1.Descriptor, from string) error {
	log.WithFields(map[string]any{"digest": desc.Digest, "from": from}).Debug("mounting descriptor")

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := c.fetchJson(ctx, desc, from, &index); err != nil {
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

	repoUrl := repoRef.URL(c.plainHttp)
	resp, err := c.client.Post(ctx, repoUrl.Mount(desc.Digest, from), nil, packerhttp.WithContentType(desc.MediaType))
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
	if err := c.fetchJson(ctx, desc, from, &manifest); err != nil {
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

// fetchJson decodes the manifest or index desc points at, reading it from the
// repository named by from — the one being mounted out of, which is not
// necessarily the client's own repository.
func (c Client) fetchJson(ctx context.Context, desc ocispecv1.Descriptor, from string, v any) error {
	ref := reference.Reference{Path: from, Ref: desc.Digest.String()}

	reader, err := c.Fetch(ctx, ref.WithDescriptor(desc))
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	return json.NewDecoder(reader).Decode(v)
}

func (c Client) FetchReference(ctx context.Context, ref reference.Reference) (ocispecv1.Descriptor, io.ReadCloser, error) {
	fields := map[string]any{"image": ref.Path, "reference": ref.Ref}
	log.WithFields(fields).Debug("fetching reference")

	desc, _, err := c.resolve(ctx, ref)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	fields["digest"] = desc.Digest
	fields["size"] = desc.Size
	log.WithFields(fields).Debug("reference found")

	repoUrl := c.ref.Merge(ref).URL(c.plainHttp)
	resp, err := c.client.Get(ctx, repoUrl.Manifests(), packerhttp.WithAccept(desc.MediaType))
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return ocispecv1.Descriptor{}, nil, packerhttp.NewUnexpectedStatusErr(resp)
	}

	return desc, resp.Body, nil
}

// isManifest reports whether mt names a manifest or an index. A registry keeps
// those in a namespace of their own: they are readable at /manifests/<digest>
// and are *not* served from /blobs/<digest>, however they were uploaded.
func isManifest(mt string) bool {
	switch mt {
	case ocispecv1.MediaTypeImageManifest, ocispecv1.MediaTypeImageIndex,
		images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList:
		return true
	}
	return false
}

// Fetch returns the content addressed by ref, reading it from whichever endpoint
// holds it. Nothing in a digest says whether it names a manifest or a blob, so
// the caller has to attach the descriptor with Reference.WithDescriptor when it
// is asking for a manifest; without one, Fetch can only assume a blob.
func (c Client) Fetch(ctx context.Context, ref reference.Reference) (io.ReadCloser, error) {
	repoUrl := c.ref.Merge(ref).URL(c.plainHttp)

	url, opts := repoUrl.Blobs(), []packerhttp.RequestOption(nil)
	if desc := ref.Descriptor(); isManifest(desc.MediaType) {
		url = repoUrl.Manifests()
		opts = append(opts, packerhttp.WithAccept(desc.MediaType))
	}

	resp, err := c.client.Get(ctx, url, opts...)
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

	ref := reference.Reference{Ref: desc.Digest.String()}
	if ok, err := c.Exists(ctx, ref); err != nil {
		return err
	} else if ok {
		return registry.ErrAlreadyExists
	}

	repoUrl, err := c.urlFromReference(reference.Reference{})
	if err != nil {
		return err
	}

	resp, err := c.client.Post(ctx, repoUrl.Uploads(), nil)
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

	u := repoUrl.UploadsUrl()
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
	if !isManifest(desc.MediaType) {
		return fmt.Errorf("unsupported media type: %s", desc.MediaType)
	}

	repoRef := c.ref
	repoRef.Ref = desc.Digest.String()

	r, err := c.Fetch(ctx, repoRef)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	repoUrl, _ := c.urlFromReference(reference.Reference{})
	log.WithFields(map[string]any{"url": repoUrl.Manifests(), "digest": desc.Digest}).Debug("setting tag")

	resp, err := c.client.Put(ctx, repoUrl.Manifests(), r, packerhttp.WithContentType(desc.MediaType))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return packerhttp.NewUnexpectedStatusErr(resp)
	}

	return nil
}
