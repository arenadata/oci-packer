# oci-packer

> **⚠️ Work In Progress**: This project is currently under active development. APIs and functionality may change significantly. Use at your own discretion.

A Go-based tool for building and pushing OCI (Open Container Initiative) artifacts. Enables packing multiple sources (files, directories, OCI images, remote registries) into OCI-compliant images with support for multi-platform builds and custom annotations.

## Overview

`oci-packer` simplifies the process of creating OCI artifacts from diverse sources. It provides a declarative approach to define what should be packed, how it should be built, and where it should be pushed.

## Features

- **Multiple Source Support**: Pack from local files, directories, OCI images, and remote registries
- **Multi-Platform Builds**: Create platform-specific variants of your artifacts
- **OCI Layout Support**: Native support for OCI image layout format
- **Registry Integration**: Push artifacts directly to OCI registries
- **Metadata Management**: Add custom annotations and metadata to your artifacts
- **CLI Integration**: Built with Cobra for a user-friendly command-line interface

## Declarative Configuration

`oci-packer` uses declarative YAML files to define artifact packing. Here's an example configuration:

```yaml
type: application/vnd.example.artifact
config:
  from: file://./schema.json

annotations:
  org.opencontainers.image.title: "My Artifact"

items:
  - from: file://./config.tmpl
    type: application/vnd.example.template
    annotations:
      description: "Configuration file"
  
  - from: dir://./data
    type: application/vnd.example.data
```

This declarative approach allows you to:
- Define multiple sources to pack in a single file
- Specify metadata and annotations for each component
- Configure multi-platform variants
- Maintain reproducible artifact definitions

#### TODO
- [] add remote registry support
- [] oci-layout
- [] unit tests
- commands:
  - [] proxy
  - [] copy
  - [] list components
