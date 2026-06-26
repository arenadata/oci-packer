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
	"encoding/json"
	"fmt"
	"sort"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func Copy(ctx context.Context, dst Pusher, src Fetcher, desc ocispecv1.Descriptor) error {
	fields := map[string]any{"digest": desc.Digest, "size": desc.Size, "media_type": desc.MediaType}
	log := logger.New("copy")
	log.WithFields(fields).Debug("copy artifact by descriptor")

	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := fetch(ctx, src, desc, &index); err != nil {
			return err
		}
		for _, manifest := range index.Manifests {
			if err := Copy(ctx, dst, src, manifest); err != nil {
				return err
			}
		}

	case ocispecv1.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispecv1.Manifest
		if err := fetch(ctx, src, desc, &manifest); err != nil {
			return err
		}

		if err := copyDescriptor(ctx, dst, src, manifest.Config); err != nil {
			return err
		}

		for _, layer := range manifest.Layers {
			if err := Copy(ctx, dst, src, layer); err != nil {
				return err
			}
		}

		if manifest.Subject != nil {
			if err := Copy(ctx, dst, src, *manifest.Subject); err != nil {
				return err
			}
		}
	}

	return copyDescriptor(ctx, dst, src, desc)
}

// SelectPlatform resolves an image to a single platform. If desc is an OCI
// Index (multi-platform), it returns the child manifest descriptor that best
// matches the given platform, erroring if none match. If desc is already a
// single manifest, it is returned unchanged.
func SelectPlatform(ctx context.Context, src Fetcher, desc ocispecv1.Descriptor, match platforms.MatchComparer) (ocispecv1.Descriptor, error) {
	switch desc.MediaType {
	case ocispecv1.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispecv1.Index
		if err := fetch(ctx, src, desc, &index); err != nil {
			return ocispecv1.Descriptor{}, err
		}

		var matched []ocispecv1.Descriptor
		for _, m := range index.Manifests {
			if m.Platform != nil && match.Match(*m.Platform) {
				matched = append(matched, m)
			}
		}
		if len(matched) == 0 {
			return ocispecv1.Descriptor{}, fmt.Errorf("no manifest in index matches the requested platform")
		}

		sort.SliceStable(matched, func(i, j int) bool {
			return match.Less(*matched[i].Platform, *matched[j].Platform)
		})
		return matched[0], nil
	}

	return desc, nil
}

func copyDescriptor(ctx context.Context, dst Pusher, src Fetcher, desc ocispecv1.Descriptor) error {
	fields := map[string]any{"digest": desc.Digest, "media_type": desc.MediaType}
	log := logger.New("copy_descriptor")
	log.WithFields(fields).Debug("copying artifact")

	ref := reference.Reference{Ref: desc.Digest.String()}
	r, err := src.Fetch(ctx, ref.WithDescriptor(desc))
	if err != nil {
		log.WithError(err).WithFields(fields).Error("error fetching descriptor")
		return err
	}
	defer func() { _ = r.Close() }()

	log.WithFields(fields).Info("copy artifact")
	if err = dst.Push(ctx, desc, r); err != nil {
		if !IsAlreadyExists(err) {
			return err
		}
		log.WithFields(fields).Info("file already exists")
	}
	return nil
}

func fetch(ctx context.Context, src Fetcher, desc ocispecv1.Descriptor, v any) error {
	reader, err := src.Fetch(ctx, reference.Reference{Ref: desc.Digest.String()})
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	return json.NewDecoder(reader).Decode(v)
}
