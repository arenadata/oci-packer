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

package packer

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"

	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/utils"

	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Pusher interface {
	Push(ctx context.Context, pusher registry.Pusher, opts ...registry.PushOption) (ocispec.Descriptor, error)
}

type index struct {
	Type string

	Manifests   []manifest
	Annotations map[string]string
}

func (i index) Push(ctx context.Context, pusher registry.Pusher, opts ...registry.PushOption) (ocispec.Descriptor, error) {
	index := ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: i.Type,
		Annotations:  i.Annotations,
	}

	for _, m := range i.Manifests {
		desc, err := m.push(ctx, pusher)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		if len(m.Platform) > 0 {
			p, err := platforms.Parse(m.Platform)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
			desc.Platform = &p
		}

		index.Manifests = append(index.Manifests, desc)
	}

	desc, r, err := newDescriptorWithReader(index, index.MediaType)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if err = pusher.PushReference(ctx, desc, r, opts...); err != nil {
		return ocispec.Descriptor{}, err
	}
	desc.ArtifactType = index.ArtifactType

	return desc, nil
}

type manifest struct {
	Metadata
	Type        string
	Descriptors []Descriptor
	Platform    string
}

func (m manifest) Push(ctx context.Context, pusher registry.Pusher, opts ...registry.PushOption) (ocispec.Descriptor, error) {
	manifest, err := m.toOCIManifest(ctx, pusher)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	desc, r, err := newDescriptorWithReader(manifest, manifest.MediaType)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	err = pusher.PushReference(ctx, desc, r, opts...)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	desc.ArtifactType = manifest.ArtifactType

	return desc, nil
}

func (m manifest) toOCIManifest(ctx context.Context, pusher registry.Pusher) (ocispec.Manifest, error) {
	var err error
	var manifestConfig ocispec.Descriptor
	var manifestConfigRC io.Reader
	if m.Config == nil {
		manifestConfig = ocispec.DescriptorEmptyJSON
		manifestConfigRC = bytes.NewReader(ocispec.DescriptorEmptyJSON.Data)
	} else {
		manifestConfig, manifestConfigRC, err = m.Config.ToOciDescriptor()
		if err != nil {
			return ocispec.Manifest{}, err
		}
	}

	if err = pusher.Push(ctx, manifestConfig, manifestConfigRC); err != nil {
		return ocispec.Manifest{}, err
	}

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: m.Type,
		Config:       manifestConfig,
		Annotations:  m.Annotations,
	}

	for _, d := range m.Descriptors {
		desc, r, err := d.ToOciDescriptor()
		if err != nil {
			return ocispec.Manifest{}, err
		}
		if err = pusher.Push(ctx, desc, r); err != nil {
			return ocispec.Manifest{}, err
		}

		manifest.Layers = append(manifest.Layers, desc)
	}

	return manifest, nil
}

func (m manifest) push(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error) {
	manifest, err := m.toOCIManifest(ctx, pusher)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	desc, r, err := newDescriptorWithReader(manifest, manifest.MediaType)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if err = pusher.Push(ctx, desc, io.NopCloser(r)); err != nil {
		return ocispec.Descriptor{}, err
	}
	desc.ArtifactType = manifest.ArtifactType

	return desc, nil
}

func newDescriptorWithReader(manifest any, mt string) (ocispec.Descriptor, io.Reader, error) {
	b, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return utils.NewDescriptorFromBytes(mt, b),
		io.NopCloser(bytes.NewReader(b)),
		nil
}
