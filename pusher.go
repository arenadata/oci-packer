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
	"path/filepath"
	"strings"
	"time"

	"github.com/arenadata/oci-packer/pkg/registry"

	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/docker/go-units"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Pusher interface {
	Push(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error)
}

type index struct {
	Type string

	Manifests   []manifest
	Annotations map[string]string
}

func (i index) Push(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error) {
	index := ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: i.Type,
		Annotations:  i.Annotations,
	}

	for _, m := range i.Manifests {
		desc, err := m.pushManifest(ctx, pusher)
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

	return commit(ctx, pusher, index, index.MediaType, index.ArtifactType)
}

func commit(ctx context.Context, pusher registry.Pusher, v any, mt, at string) (ocispec.Descriptor, error) {
	desc, r, err := newDescriptorWithReader(v, mt)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err = pusher.Push(ctx, desc, r); err != nil {
		if !registry.IsAlreadyExists(err) {
			return ocispec.Descriptor{}, err
		}
	}

	if err = pusher.SetTag(ctx, desc); err != nil {
		return ocispec.Descriptor{}, err
	}
	desc.ArtifactType = at

	return desc, nil
}

type manifest struct {
	Metadata
	Type        string
	Descriptors []Descriptor
	Platform    string
}

func (m manifest) Push(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error) {
	configDescriptor, err := m.pushConfig(ctx, pusher)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: m.Type,
		Config:       configDescriptor,
		Annotations:  m.Annotations,
	}

	for _, d := range m.Descriptors {
		desc, err := m.pushFile(ctx, pusher, d)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		manifest.Layers = append(manifest.Layers, desc)
	}

	return commit(ctx, pusher, manifest, manifest.MediaType, manifest.ArtifactType)
}

func (m manifest) pushFile(ctx context.Context, pusher registry.Pusher, d Descriptor) (ocispec.Descriptor, error) {
	desc, r, err := d.ToOciDescriptor()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() { _ = r.Close() }()

	from := strings.TrimPrefix(d.From, FileSchema)
	fields := log.Fields{
		"size":     units.BytesSize(float64(desc.Size)),
		"filepath": filepath.Clean(from),
	}

	log.L.WithFields(fields).Infof("upload artefact")

	now := time.Now()
	err = pusher.Push(ctx, desc, r)

	fields["digest"] = desc.Digest
	fields["duration"] = time.Since(now).Round(time.Millisecond).String()

	if err != nil {
		if !registry.IsAlreadyExists(err) {
			log.L.WithFields(fields).Error("uploaded artefact failed")
			return ocispec.Descriptor{}, err
		}
		log.L.WithFields(fields).Warning("artefact already uploaded")
	} else {
		log.L.WithFields(fields).Infof("artefact uploaded")
	}

	return desc, nil
}

func (m manifest) pushConfig(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error) {
	if m.Config == nil {
		r := bytes.NewReader(ocispec.DescriptorEmptyJSON.Data)
		if err := pusher.Push(ctx, ocispec.DescriptorEmptyJSON, r); err != nil {
			if !registry.IsAlreadyExists(err) {
				return ocispec.Descriptor{}, err
			}
		}
		return ocispec.DescriptorEmptyJSON, nil
	}

	return m.pushFile(ctx, pusher, m.Config.ToDescriptor())
}

func (m manifest) pushManifest(ctx context.Context, pusher registry.Pusher) (ocispec.Descriptor, error) {
	manifest, err := m.Push(ctx, pusher)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	desc, r, err := newDescriptorWithReader(manifest, manifest.MediaType)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if err = pusher.Push(ctx, desc, r); err != nil {
		if !registry.IsAlreadyExists(err) {
			return ocispec.Descriptor{}, err
		}
	}
	desc.ArtifactType = manifest.ArtifactType

	return desc, nil
}

func newDescriptorWithReader(manifest any, mt string) (ocispec.Descriptor, io.Reader, error) {
	b, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return NewDescriptorFromBytes(mt, b), bytes.NewReader(b), nil
}
