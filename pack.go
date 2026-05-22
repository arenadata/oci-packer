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
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/registry/reference"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"
)

type Pack struct {
	Metadata `json:",inline" yaml:",inline"`

	Type  string       `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Items []Descriptor `json:"items" yaml:"items" jsonschema:"minItems=1"`
}

type Metadata struct {
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type Descriptor struct {
	From   string            `yaml:"from" json:"from"`
	Type   string            `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`
	Config *ConfigDescriptor `yaml:"config,omitempty" json:"config,omitempty"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type ConfigDescriptor struct {
	From string `yaml:"from" json:"from"`
	Type string `yaml:"type,omitempty" json:"type,omitempty" jsonschema:"description=Set artifactType"`

	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" jsonschema:"pattern=^([A-Za-z0-9_-]+)(?:\\(([A-Za-z0-9_.-]*)\\))?/([A-Za-z0-9_-]+)$"`

	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

func (d Descriptor) FileToOciDescriptor() (ocispecv1.Descriptor, io.ReadCloser, error) {
	var desc ocispecv1.Descriptor
	var reader io.ReadCloser

	if !reference.IsFile(d.From) {
		return ocispecv1.Descriptor{}, nil, fmt.Errorf("%s is not a file", d.From)
	}

	from := strings.TrimPrefix(d.From, reference.FileSchema.String())
	fi, err := os.Open(from)
	if err != nil {
		return ocispecv1.Descriptor{}, nil, err
	}

	desc, err = NewDescriptorFromFileDescriptor(fi)
	if err != nil {
		_ = fi.Close()
		return ocispecv1.Descriptor{}, nil, err
	}

	reader = fi

	desc.Annotations = d.Annotations
	desc.ArtifactType = d.Type

	return desc, reader, nil
}

func (p Pack) Validate() error {
	log := logger.New("validate")
	log.Debug("validating pack configuration")

	if len(p.Items) == 0 {
		err := fmt.Errorf("pack file must contain at least one item")
		log.WithError(err).Error("items validation failed")
		return err
	}

	if err := p.validateMetadata(); err != nil {
		log.WithError(err).Error("metadata validation failed")
		return err
	}

	for i, item := range p.Items {
		if len(item.From) == 0 {
			err := fmt.Errorf("item[%d]: 'from' field is required", i)
			log.WithError(err).Error("item validation failed")
			return err
		}

		if !isValidSource(item.From) {
			err := fmt.Errorf("item[%d]: invalid source format '%s'", i, item.From)
			log.WithError(err).Error("item validation failed")
			return err
		}

		if err := item.validateDescriptor(); err != nil {
			err = fmt.Errorf("item[%d]: %v", i, err)
			log.WithError(err).Error("Loader", "item descriptor validation failed")
			return err
		}
	}

	log.Debug("pack configuration validated successfully")

	return nil
}

func (p Pack) validateMetadata() error {
	if p.Config != nil {
		if len(p.Config.From) == 0 {
			return fmt.Errorf("metadata.config: 'from' field is required")
		}
		if !isValidSource(p.Config.From) {
			return fmt.Errorf("metadata.config: invalid source format '%s'", p.Config.From)
		}
	}
	return nil
}

func (d Descriptor) validateDescriptor() error {
	if d.Config != nil {
		if len(d.Config.From) == 0 {
			return fmt.Errorf("config: 'from' field is required")
		}
		if !isValidSource(d.Config.From) {
			return fmt.Errorf("config: invalid source format '%s'", d.Config.From)
		}
	}
	return nil
}

func isValidSource(from string) bool {
	return reference.IsFile(from) || reference.IsDir(from) || reference.IsOCI(from) ||
		reference.IsS3(from) || reference.IsHTTP(from)
}

func (d ConfigDescriptor) ToDescriptor() Descriptor {
	return Descriptor{
		From:        d.From,
		Type:        d.Type,
		Platform:    d.Platform,
		Annotations: d.Annotations,
	}
}

func LoadFromFile(path string) (*Pack, error) {
	log := logger.New("load_pack_from_file")
	fields := map[string]any{"path": path}

	log.WithFields(fields).Debug("open Pack file")
	f, err := os.Open(path)
	if err != nil {
		log.WithError(err).WithFields(fields).Error("failed to open pack file")
		return nil, err
	}
	defer func() { _ = f.Close() }()

	log.WithFields(fields).Debug("decoding pack file YAML")
	var uploader Pack
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err = dec.Decode(&uploader); err != nil {
		log.WithError(err).WithFields(fields).Error("failed to decode pack file")
		return nil, err
	}

	log.WithFields(fields).Debug("pack file decoded successfully")

	return &uploader, nil
}
