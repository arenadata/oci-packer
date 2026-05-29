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
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/containerd/platforms"
	"github.com/docker/go-units"
	"github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type Pusher interface {
	Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error)
}

type index struct {
	Type string

	Manifests   []manifest
	Annotations map[string]string
}

func (i index) Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	log := logger.New("index_push")
	log.Debug("building index OCI-manifest for pushing")

	index := ocispecv1.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispecv1.MediaTypeImageIndex,
		ArtifactType: i.Type,
		Annotations:  i.Annotations,
	}

	for _, m := range i.Manifests {
		log.Debug("push manifest")

		desc, err := m.Push(ctx, pusher)
		if err != nil {
			log.WithError(err).Errorf("failed to push manifest")
			return ocispecv1.Descriptor{}, err
		}

		if len(m.Platform) > 0 {
			p, err := platforms.Parse(m.Platform)
			if err != nil {
				log.WithError(err).WithField("platform", m.Platform).Errorf("failed to parse platform")
				return ocispecv1.Descriptor{}, err
			}
			desc.Platform = &p
			log.WithField("platform", platforms.FormatAll(p)).Debug("descriptor created for platform")
		}

		index.Manifests = append(index.Manifests, desc)
	}

	log.WithField("manifests_count", len(index.Manifests)).Debug("committing index manifest")
	return commit(ctx, pusher, index, index.MediaType, index.ArtifactType)
}

type manifest struct {
	Metadata
	Type        string
	Descriptors []Descriptor
	Platform    string
}

func (m manifest) Push(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	log := logger.New("manifest_push")
	log.Debug("building manifest")

	configDescriptor, err := m.pushConfig(ctx, pusher)
	if err != nil {
		log.WithError(err).WithField("digest", configDescriptor.Digest).Error("failed to push config")
		return ocispecv1.Descriptor{}, err
	}

	log.WithField("digest", configDescriptor.Digest).Debug("config pushed successfully")

	manifest := ocispecv1.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispecv1.MediaTypeImageManifest,
		ArtifactType: m.Type,
		Config:       configDescriptor,
		Annotations:  m.Annotations,
	}

	for _, d := range m.Descriptors {
		handler := m.pushFile
		if reference.IsOCI(d.From) {
			handler = m.mount
		}

		desc, err := handler(ctx, pusher, d)
		if err != nil {
			return ocispecv1.Descriptor{}, err
		}
		log.WithField("digest", desc.Digest).Debug("pushed descriptor layer")
		manifest.Layers = append(manifest.Layers, desc)
	}

	log.WithField("layers_count", len(manifest.Layers)).Debug("committing manifest")
	return commit(ctx, pusher, manifest, manifest.MediaType, manifest.ArtifactType)
}

func (m manifest) mount(ctx context.Context, pusher registry.Pusher, d Descriptor) (ocispecv1.Descriptor, error) {
	log := logger.New("mount_repository")

	ref := strings.TrimPrefix(d.From, reference.RegistryScheme.String())
	parsedRef, err := reference.ParseRegistryReference(ref)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}

	desc, err := pusher.MountFrom(ctx, parsedRef)
	if err != nil {
		log.WithError(err).WithField("from", ref).Error("failed to mount repository")
	} else {
		log.WithField("from", ref).Debug("mounted repository")
	}
	return desc, err
}

func (m manifest) pushFile(ctx context.Context, pusher registry.Pusher, d Descriptor) (ocispecv1.Descriptor, error) {
	log := logger.New("push_file")

	desc, r, err := d.FileToOciDescriptor()
	if err != nil {
		log.WithError(err).Error("failed to prepare layer descriptor")
		return ocispecv1.Descriptor{}, err
	}
	defer func() { _ = r.Close() }()
	log.WithField("digest", desc.Digest).Debug("prepared layer")

	fields := map[string]any{
		"digest":   desc.Digest,
		"size":     units.BytesSize(float64(desc.Size)),
		"filepath": strings.TrimPrefix(d.From, reference.FileSchema.String()),
	}

	log.WithFields(fields).Debug("upload file")

	now := time.Now()
	err = pusher.Push(ctx, desc, r)

	fields["digest"] = desc.Digest
	fields["duration"] = time.Since(now).Round(time.Millisecond).String()

	if err != nil {
		if !registry.IsAlreadyExists(err) {
			log.WithError(err).WithFields(fields).Error("uploaded file failed")
			return ocispecv1.Descriptor{}, err
		}
		log.WithFields(fields).Info("file already uploaded")
	} else {
		log.WithFields(fields).Info("file uploaded")
	}

	return desc, nil
}

func (m manifest) pushConfig(ctx context.Context, pusher registry.Pusher) (ocispecv1.Descriptor, error) {
	if m.Config == nil {
		r := bytes.NewReader(ocispecv1.DescriptorEmptyJSON.Data)
		if err := pusher.Push(ctx, ocispecv1.DescriptorEmptyJSON, r); err != nil {
			if !registry.IsAlreadyExists(err) {
				return ocispecv1.Descriptor{}, err
			}
		}
		return ocispecv1.DescriptorEmptyJSON, nil
	}

	return m.pushFile(ctx, pusher, m.Config.ToDescriptor())
}

func newDescriptorWithReader(manifest any, mt string) (ocispecv1.Descriptor, io.Reader, error) {
	b, err := json.Marshal(manifest)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	return NewDescriptorFromBytes(mt, b), bytes.NewReader(b), nil
}

func commit(ctx context.Context, pusher registry.Pusher, v any, mt, at string) (ocispecv1.Descriptor, error) {
	desc, r, err := newDescriptorWithReader(v, mt)
	if err != nil {
		return ocispecv1.Descriptor{}, err
	}
	if err = pusher.Push(ctx, desc, r); err != nil {
		if !registry.IsAlreadyExists(err) {
			return ocispecv1.Descriptor{}, err
		}
	}
	desc.ArtifactType = at

	return desc, nil
}
