# oci-packer

> ⚠️ **Проект находится в активной разработке.** API и функциональность могут существенно изменяться. Используйте с
> осторожностью.

Go-инструмент для создания и публикации OCI-артефактов из разнородных источников.

[🇷🇺 Русский](#русский) | [🇬🇧 English](#english)

---

# Русский

## Описание

`oci-packer` — инструмент на языке Go для упаковки произвольных источников (файлы, директории, OCI-образы, HTTP-ресурсы)
в OCI-совместимые артефакты и их публикации в реестры контейнеров.

Проект разработан компанией [Arenadata](https://arenadata.io) и предназначен для управления дистрибуцией артефактов в
инфраструктуре на базе OCI (Open Container Initiative), в том числе для сценариев, выходящих за рамки классических
Docker-образов: конфигурации, шаблоны, бинарные данные, слои с зависимостями.

## Назначение

[ORAS (OCI Registry As Storage)](https://oras.land) — референсный CLI-инструмент CNCF для работы с OCI-артефактами:
позволяет загружать и скачивать отдельные файлы или группы файлов с явным указанием их типов. `oci-packer` решает
смежную задачу, но с принципиально иным подходом:

| Критерий                        | ORAS                                                  | oci-packer                                            |
|---------------------------------|-------------------------------------------------------|-------------------------------------------------------|
| **Описание артефакта**          | Императивное: файлы и типы передаются аргументами CLI | Декларативное: YAML Pack-файл описывает всю структуру |
| **Источники**                   | Локальные файлы и директории                          | Файлы, директории, HTTP/HTTPS, OCI-реестры, S3        |
| **Мультиплатформенность**       | Ограниченная поддержка индексов                       | Нативное создание OCI Index с `platform`-вариантами   |
| **Конфигурационный дескриптор** | Фиксированный пустой config                           | Произвольный `config`-дескриптор из любого источника  |
| **Интеграция в CI/CD**          | Push-команды в скриптах                               | Один файл — полная спецификация артефакта             |
| **Монтирование из реестра**     | Нет                                                   | Поддержка `oci://`-источников с mount из реестра      |
| **Зрелость**                    | Стабильный, CNCF Sandbox                              | Work In Progress                                      |

`oci-packer` ориентирован на сценарии, где структура артефакта сложна и воспроизводима: несколько источников разных
типов, мультиплатформенные сборки, кастомные config-объекты — всё описывается в одном файле и воспроизводится
детерминированно.

## Возможности

- **Множество типов источников**: файлы (`file://`), директории (`dir://`), HTTP/HTTPS-ресурсы (`http://`, `https://`),
  OCI/Docker-реестры (`oci://`, `docker://`), S3 (`s3://`, в разработке)
- **Мультиплатформенные сборки**: создание индекс-манифестов с вариантами для разных архитектур (linux/amd64,
  linux/arm64 и т.д.)
- **OCI Layout**: поддержка локального формата `oci-layout` с распаковкой слоёв
- **Публикация в реестр**: прямая загрузка артефактов через OCI Distribution API
- **Аннотации и метаданные**: гибкое управление OCI-аннотациями на уровне артефакта, манифеста и каждого слоя
- **Конфигурационные дескрипторы**: кастомные `config`-объекты вместо стандартного пустого конфига
- **CLI-интерфейс**: построен на базе [Cobra](https://github.com/spf13/cobra)
- **Структурированное логирование**: через [logrus](https://github.com/sirupsen/logrus)

## Архитектура

```
oci-packer/
├── cmd/                    # CLI-команды (pack, copy, proxy, ...)
├── internal/
│   └── logger/             # Внутренний логгер
├── pkg/
│   ├── registry/           # Клиент OCI-реестра, pusher/puller
│   │   └── reference/      # Парсинг схем источников (file://, dir://, oci://, ...)
│   └── http/               # HTTP-клиент
├── builder.go              # Оркестрация сборки (index / manifest)
├── handlers.go             # Обработчики источников (file, dir, http, oci)
├── pack.go                 # Модели данных, валидация, загрузка Pack-файла
├── pusher.go               # Публикация манифестов и слоёв в реестр
└── utils.go                # Утилиты: digest, MediaType, дескрипторы
```

**Ключевые типы:**

- `Pack` — корневая структура, описывающая артефакт (тип, метаданные, список элементов)
- `Descriptor` — описание одного источника (откуда взять, тип, платформа, аннотации)
- `ConvertHandler` — функциональный тип, преобразующий источник в набор дескрипторов
- `Pusher` — интерфейс для записи манифеста/индекса в реестр

**Поток выполнения:**

```
Pack-файл (YAML) → Validate → handleItem (выбор хендлера по схеме)
    ├── file://   → fileHandler
    ├── dir://    → walkDirHandler (обход директории)
    ├── http(s):// → httpHandler (скачивание → временный файл)
    └── oci://    → ociHandler (монтирование из реестра)
→ makeManifest / makeIndex → Push → OCI Registry / OCI Layout
```

## Формат Pack-файла

Pack-файл — декларативный YAML, описывающий содержимое артефакта:

```yaml
# Тип артефакта (MIME-тип)
type: application/vnd.example.artifact

# Глобальные аннотации
annotations:
  org.opencontainers.image.title: "Мой артефакт"
  org.opencontainers.image.vendor: "Arenadata"

# Конфигурационный дескриптор (опционально)
config:
  from: file://schema.json
  type: application/vnd.example.schema

# Список элементов для упаковки
items:
  # Файл
  - from: file://config.tmpl
    type: application/vnd.example.template
    annotations:
      description: "Конфигурационный шаблон"

  # Директория (все файлы рекурсивно)
  - from: dir://data/
    type: application/vnd.example.data

  # HTTP-ресурс
  - from: https://example.com/binary.tar.gz

  # OCI-образ из реестра (с указанием платформы для мульти-манифеста)
  - from: oci://registry.example.com/myimage:latest
    platform: linux/amd64

  - from: oci://registry.example.com/myimage:latest
    platform: linux/arm64
```

### Схемы источников

| Схема                  | Пример                         | Описание                 |
|------------------------|--------------------------------|--------------------------|
| `file://`              | `file://path/to/file.tar.gz`   | Локальный файл           |
| `dir://`               | `dir://path/to/directory/`     | Все файлы директории     |
| `http://` / `https://` | `https://example.com/data.bin` | HTTP-загрузка            |
| `oci://` / `docker://` | `oci://registry/image:tag`     | OCI/Docker-реестр        |
| `s3://`                | `s3://bucket/key`              | Amazon S3 (в разработке) |

### Мультиплатформенные артефакты

При наличии поля `platform` или источника `oci://` автоматически создаётся **OCI Index** (мульти-манифест):

```yaml
type: application/vnd.example.multiarch

items:
  - from: oci://registry.example.com/app:v1.0
    platform: linux(v8)/amd64

  - from: oci://registry.example.com/app:v1.0-arm
    platform: linux/arm64
```

## Установка

Готовые бинарники для Linux, macOS и Windows публикуются на
[странице релизов](https://github.com/arenadata/oci-packer/releases) (собираются GoReleaser).
Скачайте архив под свою платформу и распакуйте бинарь в `PATH`:

```bash
tar -xzf oci-packer_*_linux_amd64.tar.gz
sudo install oci-packer /usr/local/bin/
```

Через Go:

```bash
go install github.com/arenadata/oci-packer/cmd/oci-packer@latest
```

Или сборка из исходников:

```bash
git clone https://github.com/arenadata/oci-packer.git
cd oci-packer
go build ./cmd/...
```

**Требования:** Go 1.26+

## Документация

Полная документация по командам и решение проблем — в каталоге [`docs/`](docs/README.md):

- [Хаб документации](docs/README.md) — указатель на все команды
- [`pack`](docs/pack.md) · [`copy`](docs/copy.md) · [`proxy`](docs/proxy.md) · [`mount` / `umount`](docs/mount.md)
- [Troubleshooting](docs/troubleshooting.md) — типичные ошибки и их устранение

## Использование

### Упаковка артефакта

```bash
# Упаковать и опубликовать в реестр
oci-packer -f artifact.yaml registry.example.com/myartifact:v1.0

# Указать временную директорию для HTTP-загрузок
oci-packer -f artifact.yaml registry.example.com/myartifact:v1.0 --tmp-dir /tmp/packer
```

### Копирование между реестрами

```bash
oci-packer copy \
  cr://source-registry.example.com/image:tag \
  oci://target/directory:image:tag

# Скопировать только одну платформу из мультиплатформенного образа
oci-packer copy --platform linux/arm64 \
  cr://source-registry.example.com/image:tag \
  oci://target/directory:image:tag
```

При `--platform` из OCI Index выбирается манифест нужной платформы, и в назначение
копируется одноплатформенный образ. Формат — `ОС/архитектура[/вариант]`, напр.
`linux/amd64`, `linux/arm64`, `linux/arm/v7`.

### Режим прокси

```bash
oci-packer proxy cr://registry.example.com
```

### Монтирование слоёв (`mount` / `umount`, только Linux)

Команда `mount` монтирует слои **образа** из распакованного OCI Layout (созданного через
`copy --unpack`) в директорию **только для чтения** через overlayfs. Один layout может
содержать много образов, поэтому ссылка указывает конкретный образ:
`oci://<каталог>:<репозиторий>:<тег>`. Записываемые пути (`/tmp`, `/run`, `/var/tmp`)
автоматически монтируются как tmpfs.

```bash
# Скачать образ в OCI Layout с распаковкой слоёв
oci-packer copy --unpack cr://registry.example.com/example/service:v1 oci://./layout:example/service:v1

# Смонтировать read-only rootfs образа (нужен root)
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app

# С writable bind-директорией и постоянным systemd-юнитом
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app \
  --bind /srv/data:/var/lib/app \
  --persistent --unit-dir /etc/systemd/system --enable

# Размонтировать всё (overlay + tmpfs + bind) одной командой
sudo oci-packer umount /mnt/app
```

Подробное описание, флаги и ограничения — в [docs/mount.md](docs/mount.md).

## Зависимости

| Библиотека                  | Назначение                                |
|-----------------------------|-------------------------------------------|
| `containerd/containerd/v2`  | OCI Distribution API клиент               |
| `opencontainers/image-spec` | OCI-спецификации (дескрипторы, манифесты) |
| `opencontainers/go-digest`  | Вычисление SHA256-дайджестов              |
| `containerd/platforms`      | Парсинг и форматирование платформ         |
| `moby/go-archive`           | Работа с tar-архивами                     |
| `spf13/cobra`               | CLI-фреймворк                             |
| `sirupsen/logrus`           | Структурированное логирование             |
| `docker/go-units`           | Форматирование размеров файлов            |
| `gopkg.in/yaml.v3`          | Парсинг YAML                              |
| `klauspost/compress`        | Zstd-сжатие                               |

## Дорожная карта

- [x] Упаковка артефактов (build pack)
- [x] Копирование между реестрами и OCI Layout
- [x] Валидация Pack-файлов
- [x] Поддержка OCI Layout (с распаковкой слоёв)
- [x] Структурированное логирование
- [x] Юнит-тесты
- [x] CLI-команды: `proxy`, `copy`, `mount`, `umount`
- [ ] E2E tests
- [ ] Прогресс-бар
- [ ] Поддержка S3-хендлера
- [ ] CLI-команда: список компонентов (`list`)
- [x] CLI-команда: монтирование (`mount`, `umount`)

## Лицензия

Apache License 2.0. Подробности в файле [LICENSE](LICENSE).

## Авторы

Разработано командой [Arenadata](https://github.com/arenadata).

---

# English

## Description

`oci-packer` is a Go tool for packaging arbitrary sources — files, directories, OCI images, HTTP resources — into
OCI-compliant artifacts and pushing them to container registries.

Developed by [Arenadata](https://arenadata.io), it is designed for artifact distribution management in OCI (Open
Container Initiative) infrastructure, including use cases that go beyond classic Docker images: configurations,
templates, binary data, dependency layers.

## Purpose

[ORAS (OCI Registry As Storage)](https://oras.land) is the CNCF reference CLI for OCI artifacts: it lets you push and
pull individual files or file groups with explicit media types specified as CLI arguments. `oci-packer` solves a related
problem but with a fundamentally different approach:

| Criterion               | ORAS                                                | oci-packer                                               |
|-------------------------|-----------------------------------------------------|----------------------------------------------------------|
| **Artifact definition** | Imperative: files and types passed as CLI arguments | Declarative: YAML Pack file describes the full structure |
| **Sources**             | Local files and directories                         | Files, directories, HTTP/HTTPS, OCI registries, S3       |
| **Multi-platform**      | Limited index support                               | Native OCI Index creation with `platform` variants       |
| **Config descriptor**   | Fixed empty config                                  | Arbitrary `config` descriptor from any source            |
| **CI/CD integration**   | Push commands in scripts                            | One file — complete artifact specification               |
| **Registry mount**      | No                                                  | Supports `oci://` sources with registry-side mount       |
| **Maturity**            | Stable, CNCF Sandbox                                | Work In Progress                                         |

`oci-packer` is oriented towards scenarios where the artifact structure is complex and must be reproducible: multiple
sources of different types, multi-platform builds, custom config objects — all described in a single file and produced
deterministically.

## Features

- **Multiple source types**: files (`file://`), directories (`dir://`), HTTP/HTTPS resources (`http://`, `https://`),
  OCI/Docker registries (`oci://`, `docker://`), S3 (`s3://`, in progress)
- **Multi-platform builds**: create index manifests with variants for different architectures (linux/amd64, linux/arm64,
  etc.)
- **OCI Layout**: native support for local `oci-layout` format with layer extraction
- **Registry push**: direct artifact upload via OCI Distribution API
- **Annotations and metadata**: flexible OCI annotation management at artifact, manifest, and individual layer level
- **Config descriptors**: custom `config` objects instead of the standard empty config
- **CLI interface**: built on top of [Cobra](https://github.com/spf13/cobra)
- **Structured logging**: via [logrus](https://github.com/sirupsen/logrus)

## Architecture

```
oci-packer/
├── cmd/                    # CLI commands (pack, copy, proxy, ...)
├── internal/
│   └── logger/             # Internal logger
├── pkg/
│   ├── registry/           # OCI registry client, pusher/puller
│   │   └── reference/      # Source scheme parsers (file://, dir://, oci://, ...)
│   └── http/               # HTTP client
├── builder.go              # Build orchestration (index / manifest)
├── handlers.go             # Source handlers (file, dir, http, oci)
├── pack.go                 # Data models, validation, Pack file loading
├── pusher.go               # Manifest and layer publishing to registry
└── utils.go                # Utilities: digest, MediaType, descriptors
```

**Key types:**

- `Pack` — root structure describing the artifact (type, metadata, items list)
- `Descriptor` — description of a single source (where to fetch from, type, platform, annotations)
- `ConvertHandler` — functional type converting a source into a set of descriptors
- `Pusher` — interface for writing manifests/indexes to a registry

**Execution flow:**

```
Pack file (YAML) → Validate → handleItem (handler selection by scheme)
    ├── file://    → fileHandler
    ├── dir://     → walkDirHandler (recursive directory walk)
    ├── http(s):// → httpHandler (download → temp file)
    └── oci://     → ociHandler (mount from registry)
→ makeManifest / makeIndex → Push → OCI Registry / OCI Layout
```

## Pack File Format

A Pack file is a declarative YAML describing the artifact contents:

```yaml
# Artifact type (MIME type)
type: application/vnd.example.artifact

# Global annotations
annotations:
  org.opencontainers.image.title: "My Artifact"
  org.opencontainers.image.vendor: "Arenadata"

# Config descriptor (optional)
config:
  from: file://schema.json
  type: application/vnd.example.schema

# List of items to pack
items:
  # A file
  - from: file://config.tmpl
    type: application/vnd.example.template
    annotations:
      description: "Configuration template"

  # A directory (all files recursively)
  - from: dir://data/
    type: application/vnd.example.data

  # An HTTP resource
  - from: https://example.com/binary.tar.gz

  # OCI image from a registry (with platform for multi-manifest)
  - from: oci://registry.example.com/myimage:latest
    platform: linux/amd64

  - from: oci://registry.example.com/myimage:latest
    platform: linux/arm64
```

### Source Schemes

| Scheme                 | Example                        | Description             |
|------------------------|--------------------------------|-------------------------|
| `file://`              | `file://path/to/file.tar.gz`   | Local file              |
| `dir://`               | `dir://path/to/directory/`     | All files in directory  |
| `http://` / `https://` | `https://example.com/data.bin` | HTTP download           |
| `oci://` / `docker://` | `oci://registry/image:tag`     | OCI/Docker registry     |
| `s3://`                | `s3://bucket/key`              | Amazon S3 (coming soon) |

### Multi-platform Artifacts

When `platform` field or `oci://` source is present, an **OCI Index** (multi-manifest) is automatically created:

```yaml
type: application/vnd.example.multiarch

items:
  - from: oci://registry.example.com/app:v1.0
    platform: linux(v8)/amd64

  - from: oci://registry.example.com/app:v1.0-arm
    platform: linux/arm64
```

## Installation

Prebuilt binaries for Linux, macOS and Windows are published on the
[releases page](https://github.com/arenadata/oci-packer/releases) (built with GoReleaser).
Download the archive for your platform and put the binary on your `PATH`:

```bash
tar -xzf oci-packer_*_linux_amd64.tar.gz
sudo install oci-packer /usr/local/bin/
```

Via Go:

```bash
go install github.com/arenadata/oci-packer/cmd/oci-packer@latest
```

Or build from source:

```bash
git clone https://github.com/arenadata/oci-packer.git
cd oci-packer
go build ./cmd/...
```

**Requirements:** Go 1.26+

## Documentation

Full per-command documentation and troubleshooting live in [`docs/`](docs/README.md):

- [Documentation hub](docs/README.md) — index of all commands
- [`pack`](docs/pack.md) · [`copy`](docs/copy.md) · [`proxy`](docs/proxy.md) · [`mount` / `umount`](docs/mount.md)
- [Troubleshooting](docs/troubleshooting.md) — common errors and fixes

## Usage

### Pack an artifact

```bash
# Pack and push to registry
oci-packer -f artifact.yaml registry.example.com/myartifact:v1.0

# Specify temp directory for HTTP downloads
oci-packer -f artifact.yaml registry.example.com/myartifact:v1.0 --tmp-dir /tmp/packer
```

### Copy between registries

```bash
oci-packer copy \
  cr://source-registry.example.com/image:tag \
  oci://target/directory:image:tag

# Copy only a single platform from a multi-platform image
oci-packer copy --platform linux/arm64 \
  cr://source-registry.example.com/image:tag \
  oci://target/directory:image:tag
```

With `--platform`, the manifest for the requested platform is selected from an OCI Index and a
single-platform image is copied to the destination. The format is `os/arch[/variant]`, e.g.
`linux/amd64`, `linux/arm64`, `linux/arm/v7`.

### Proxy mode

```bash
oci-packer proxy cr://registry.example.com
```

### Mounting layers (`mount` / `umount`, Linux only)

The `mount` command mounts the layers of an **image** from an unpacked OCI Layout (produced by
`copy --unpack`) onto a directory **read-only** via overlayfs. A single layout may hold many
images, so the reference selects one: `oci://<dir>:<repository>:<tag>`. Writable paths
(`/tmp`, `/run`, `/var/tmp`) are mounted as tmpfs automatically.

```bash
# Pull an image into an OCI Layout with unpacked layers
oci-packer copy --unpack cr://registry.example.com/example/service:v1 oci://./layout:example/service:v1

# Mount the image's read-only rootfs (requires root)
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app

# With a writable bind directory and a persistent systemd unit
sudo oci-packer mount oci://./layout:example/service:v1 /mnt/app \
  --bind /srv/data:/var/lib/app \
  --persistent --unit-dir /etc/systemd/system --enable

# Unmount everything (overlay + tmpfs + bind) in one go
sudo oci-packer umount /mnt/app
```

See [docs/mount.md](docs/mount.md) for the full description, flags and limitations.

## Dependencies

| Library                     | Purpose                                |
|-----------------------------|----------------------------------------|
| `containerd/containerd/v2`  | OCI Distribution API client            |
| `opencontainers/image-spec` | OCI specs (descriptors, manifests)     |
| `opencontainers/go-digest`  | SHA256 digest computation              |
| `containerd/platforms`      | Platform string parsing and formatting |
| `moby/go-archive`           | Tar archive handling                   |
| `spf13/cobra`               | CLI framework                          |
| `sirupsen/logrus`           | Structured logging                     |
| `docker/go-units`           | File size formatting                   |
| `gopkg.in/yaml.v3`          | YAML parsing                           |
| `klauspost/compress`        | Zstd compression                       |

## Roadmap

- [x] Artifact packaging (build pack)
- [x] Copy between registries and OCI Layout
- [x] Pack file validation
- [x] OCI Layout support (with layer extraction)
- [x] Structured logging
- [x] Unit tests
- [x] CLI commands: `proxy`, `copy`, `mount`, `umount`
- [ ] E2E tests
- [ ] S3 source handler
- [ ] CLI command: list components
- [x] CLI command: mount, umount
- [ ] Progress bar

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.

## Authors

Developed by the [Arenadata](https://github.com/arenadata) team.
