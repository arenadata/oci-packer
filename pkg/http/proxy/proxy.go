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

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/arenadata/oci-packer/pkg/registry/remote"
	"github.com/containerd/platforms"

	"github.com/containerd/containerd/v2/core/images"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type FetchDescriptors func(
	ctx context.Context,
	resolver registry.Resolver,
	ref reference.Reference,
	opts map[string]string,
) ([]ocispecv1.Descriptor, error)

type Proxy struct {
	resolver registry.Resolver
	ref      reference.Reference

	fd    FetchDescriptors
	conv  RequestToReferenceConverter
	cache Cache

	// layout options
	unpack bool

	// remote client options
	plainHttp bool
	insecure  bool
	login     string
	password  string
}

func New(registryRef string, opts ...Option) (*Proxy, error) {
	scheme, err := reference.GetScheme(registryRef)
	if err != nil {
		return nil, err
	}

	if !reference.IsRegistryScheme(scheme) {
		return nil, reference.ErrSchemeUnsupported
	}

	host := strings.TrimPrefix(registryRef, scheme.String())
	if idx := strings.IndexByte(host, '/'); idx != -1 {
		host = host[:idx]
	}

	proxy := new(Proxy)
	for _, opt := range opts {
		opt(proxy)
	}

	ref := reference.Reference{Scheme: scheme}
	switch scheme {
	case reference.OciScheme:
		ref.Path = host

		var layoutOpts []layout.Option
		if proxy.unpack {
			layoutOpts = append(layoutOpts, layout.Unpack())
		}
		proxy.resolver, err = layout.Open(ref, layoutOpts...)
	case reference.RegistryScheme:
		ref.Host = host

		var remoteOpts []remote.Option
		if proxy.plainHttp {
			remoteOpts = append(remoteOpts, remote.WithPlainHttp())
		}
		if proxy.insecure {
			remoteOpts = append(remoteOpts, remote.WithInsecure())
		}
		proxy.resolver, err = remote.NewRegistryClient(ref, remoteOpts...)
	}
	if err != nil {
		return nil, err
	}

	proxy.ref = ref

	if proxy.fd == nil {
		proxy.fd = defaultFetcher
	}

	if proxy.conv == nil {
		proxy.conv = defaultConverter
	}

	if proxy.cache == nil {
		proxy.cache = newMemoryCache()
	}

	return proxy, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpError(w, http.StatusMethodNotAllowed)
		return
	}

	ref, opts, err := p.conv(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	referenceString := ref.String()
	title := opts[ocispecv1.AnnotationTitle]
	platform := opts["platform"]
	desc := p.cache.Get(referenceString, platform, title)
	if len(desc.Digest) > 0 {
		ref.Ref = desc.Digest.String()
		if err = p.fetch(r.Context(), w, desc, ref); err != nil {
			httpError(w, 0, err)
		}
		return
	}

	descList, err := p.fd(r.Context(), p.resolver, ref, opts)
	if err != nil {
		httpError(w, 0, err)
		return
	}

	var found bool
	for _, d := range descList {
		if v, ok := d.Annotations[ocispecv1.AnnotationTitle]; ok {
			var platform string
			if d.Platform != nil {
				platforms.FormatAll(*d.Platform)
			}
			p.cache.Set(referenceString, platform, title, d)

			if v == title && !found {
				desc = d
				found = true
			}
		}
	}

	if !found {
		httpError(w, http.StatusNotFound)
		return
	}

	w.Header().Set("X-Cache", "MISS")
	ref.Ref = desc.Digest.String()
	if err = p.fetch(r.Context(), w, desc, ref); err != nil && !errors.Is(err, context.Canceled) {
		httpError(w, 0, err)
	}
}

func (p *Proxy) fetch(ctx context.Context, w http.ResponseWriter, desc ocispecv1.Descriptor, ref reference.Reference) error {
	r, err := p.resolver.Fetch(ctx, ref)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	w.Header().Set("Content-Type", desc.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))

	_, _ = io.Copy(w, r)
	return nil
}

func httpError(w http.ResponseWriter, status int, errs ...error) {
	if len(errs) == 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if status > 0 {
		http.Error(w, http.StatusText(status), status)
	}

	err := errs[0].Error()
	errLower := strings.ToLower(err)
	if strings.Contains(errLower, "not found") {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	} else if strings.Contains(errLower, "required") {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	} else {
		http.Error(w, errLower, http.StatusInternalServerError)
	}
}

func defaultFetcher(
	ctx context.Context,
	resolver registry.Resolver,
	ref reference.Reference,
	opts map[string]string,
) ([]ocispecv1.Descriptor, error) {
	manifestDesc, reader, err := resolver.FetchReference(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	switch manifestDesc.MediaType {
	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispecv1.Manifest
		if err = json.NewDecoder(reader).Decode(&manifest); err != nil {
			return nil, err
		}
		return manifest.Layers, nil
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err = json.NewDecoder(reader).Decode(&index); err != nil {
			return nil, err
		}

		manifestRef := ref
		descList := make([]ocispecv1.Descriptor, 0)
		for _, desc := range index.Manifests {
			if desc.Platform != nil {
				platform, ok := opts["platform"]
				if !ok {
					return nil, errors.New("platform option is required")
				}
				if platforms.FormatAll(*desc.Platform) != platform {
					continue
				}
			}
			if desc.MediaType != ocispecv1.MediaTypeImageManifest && desc.MediaType != images.MediaTypeDockerSchema2Manifest {
				continue
			}

			manifestRef.Ref = desc.Digest.String()
			descriptors, err := defaultFetcher(ctx, resolver, manifestRef, opts)
			if err != nil {
				return nil, err
			}
			descList = append(descList, descriptors...)
		}
		return descList, nil
	}

	return nil, errors.New("unsupported media type " + manifestDesc.MediaType)
}
