# Задача: CLI-команда mount

## Цель

Добавить команду `oci-packer mount <src> <dst>`, которая монтирует слои из OCI Layout
в точку монтирования через Linux overlayfs в режиме **read-only**.
Пути, требующие записи, автоматически монтируются как tmpfs или через `--bind`.
Флаг `--persistent` генерирует systemd.mount unit.

## Контекст

- `copy --unpack` скачивает образ в oci-layout: каждый слой хранится как директория
  (`blobs/<algo>/<hex>/`), тип индекса — `MediaTypeUnpackLayout`.
- overlayfs без `upperdir`/`workdir` монтируется read-only автоматически —
  достаточно `lowerdir` + флага `ro`.
- Для директорий, требующих записи, Kubernetes использует bind mount
  поверх read-only rootfs; реализуем тот же подход через `--bind`.
- Стандартные volatile-пути (`/tmp`, `/run`, `/var/tmp`) монтируются как tmpfs
  автоматически; отключается через `--no-auto-tmpfs`.
- Если `src` разрешается в OCI Index (мульти-платформенный манифест), автоматически
  выбирается манифест для текущей платформы через `containerd/platforms`.
- `--persistent` создаёт systemd.mount unit(ы) вместо вызова `syscall.Mount`;
  `--enable` дополнительно вызывает `systemctl enable --now`.

## Сигнатура команды

```
oci-packer mount [flags] <src> <dst>
```

| Аргумент / флаг            | Пример                            | Описание |
|----------------------------|-----------------------------------|----------|
| `src`                      | `oci://./layout:tag`              | OCI Layout в unpack-режиме |
| `dst`                      | `/mnt/rootfs`                     | Точка монтирования (merged) |
| `--bind <host>:<path>`     | `--bind /data:/var/lib/app`       | Bind mount writable-директории; можно несколько раз |
| `--no-auto-tmpfs`          |                                   | Не монтировать tmpfs автоматически |
| `--tmpfs-size`             | `--tmpfs-size /tmp:512m,/run:64m` | Размер tmpfs для пути; `<path>:<size>`, повторяемый и/или через запятую (`StringSlice`); путь без размера означает «без ограничения» |
| `--verify`                 |                                   | Проверить целостность слоёв по digest перед монтированием |
| `--persistent`             |                                   | Создать systemd.mount unit(ы) вместо немедленного монтирования |
| `--unit-dir`               | `--unit-dir /etc/systemd/system`  | Куда писать unit-файлы (по умолчанию `/run/systemd/system`) |
| `--enable`                 |                                   | После записи units вызвать `systemctl enable --now` (только с `--persistent`) |

Наследует глобальные флаги `--verbose`.

---

## Как выглядит результат

### Без --persistent (немедленное монтирование)

```bash
# 1. Overlay read-only
mount -t overlay overlay \
  -o lowerdir=/blobs/sha256/layerN:.../layer1,ro \
  /mnt/rootfs

# 2. Auto-tmpfs (если не --no-auto-tmpfs)
mount -t tmpfs tmpfs /mnt/rootfs/tmp
mount -t tmpfs tmpfs /mnt/rootfs/run
mount -t tmpfs tmpfs /mnt/rootfs/var/tmp

# 3. Bind mount для каждого --bind
mount --bind /data /mnt/rootfs/var/lib/app
```

### С --persistent (systemd units)

Имя файла из пути через `systemd-escape --path`:
`/mnt/rootfs` → `mnt-rootfs.mount`.

**`mnt-rootfs.mount`** (overlay):
```ini
[Unit]
Description=OCI overlay mount: <src> → /mnt/rootfs

[Mount]
What=overlay
Where=/mnt/rootfs
Type=overlay
Options=lowerdir=/blobs/sha256/layerN:.../layer1,ro

[Install]
WantedBy=multi-user.target
```

**`mnt-rootfs-tmp.mount`** (auto-tmpfs, генерируется для каждого volatile-пути):
```ini
[Unit]
Description=tmpfs on /mnt/rootfs/tmp
After=mnt-rootfs.mount
BindsTo=mnt-rootfs.mount

[Mount]
What=tmpfs
Where=/mnt/rootfs/tmp
Type=tmpfs
Options=size=512m   # если для /tmp задан --tmpfs-size /tmp:512m

[Install]
WantedBy=multi-user.target
```

**`mnt-rootfs-var-lib-app.mount`** (bind, для каждого `--bind`):
```ini
[Unit]
Description=Bind mount /data → /mnt/rootfs/var/lib/app
After=mnt-rootfs.mount
BindsTo=mnt-rootfs.mount

[Mount]
What=/data
Where=/mnt/rootfs/var/lib/app
Type=none
Options=bind

[Install]
WantedBy=multi-user.target
```

---

## Подзадачи

### 1. Вспомогательные методы чтения манифеста из Layout

**Файл:** `pkg/registry/oci-layout/layout.go`

```go
func (l Layout) readManifestBlob(desc ocispecv1.Descriptor) (ocispecv1.Manifest, error)
func (l Layout) resolveManifest(ctx context.Context, ref reference.Reference) (ocispecv1.Manifest, error)
```

`readManifestBlob`:
- Открывает `l.getBlobPath(desc.Digest)`, декодирует JSON → `ocispecv1.Manifest`
- Возвращает ошибку если `desc.MediaType` — Index (вызывающий обязан разрешить Index заранее)

`resolveManifest`:
- Вызывает `l.Resolve(ctx, ref)` → `desc`
- Если `desc.MediaType` — `MediaTypeImageIndex` или `MediaTypeDockerSchema2ManifestList`:
  - Читает Index из blob'а
  - Выбирает манифест через `platforms.Only(platforms.DefaultSpec())` из `containerd/platforms`
  - Рекурсивно вызывает `readManifestBlob` для найденного дескриптора
- Иначе — вызывает `readManifestBlob` напрямую
- Используется везде, где нужен конкретный манифест (LayerDirs, VerifyLayers)

---

### 2. Метод Layout.LayerDirs

**Файл:** `pkg/registry/oci-layout/layout.go`

```go
// LayerDirs returns absolute paths to unpacked layer directories
// in bottom-to-top order (as in the manifest).
// If src resolves to an OCI Index, the manifest matching the current
// platform is selected automatically.
func (l Layout) LayerDirs(ctx context.Context, ref reference.Reference) ([]string, error)
```

1. `resolveManifest(ctx, ref)` → `manifest` (с автовыбором платформы из Index)
2. Проверить `l.unpack == true`; иначе: `"layout is not in unpack mode; re-copy with --unpack"`
3. Для каждого `manifest.Layers[i]` собрать `getBlobPath(layer.Digest)`, проверить `IsDir()`
4. Вернуть `[]string{bottom, ..., top}` (порядок манифеста = bottom-to-top)

---

### 3. Метод Layout.VerifyLayers

**Файл:** `pkg/registry/oci-layout/layout.go`

```go
// VerifyLayers recomputes the digest of each layer and compares it to
// the digest recorded in the manifest. Returns an error on first mismatch.
// If src resolves to an OCI Index, the manifest matching the current
// platform is selected automatically.
func (l Layout) VerifyLayers(ctx context.Context, ref reference.Reference) error
```

Алгоритм для каждого слоя из `manifest.Layers` (получен через `resolveManifest`):

**Режим tar** (`l.unpack == false`):
- Открыть blob-файл `getBlobPath(layer.Digest)`
- Посчитать sha256 потоком → сравнить с `layer.Digest`
- Сложность: O(размер слоя), быстро

**Режим unpack** (`l.unpack == true`):
- Blob-директория уже распакована; исходный сжатый tar недоступен
- Запустить `archive.Tar(blobPath, compression.None)` → получить tar-поток
- Дополнительно сжать в зависимости от `layer.MediaType`:
  - `MediaTypeImageLayerGzip` → gzip
  - `MediaTypeImageLayerZstd` → zstd (детерминированный уровень сжатия)
  - `MediaTypeImageLayer` → без сжатия
- Посчитать sha256 сжатого потока → сравнить с `layer.Digest`
- Сложность: O(размер слоя) + сжатие; для больших образов медленно;
  предупреждение в логе при запуске верификации

Ошибка возвращается как:
```
layer[2] digest mismatch: expected sha256:abc…, got sha256:def…
```

---

### 4. Пакет pkg/overlay

#### 4а. mount.go (только Linux)

**Файл:** `pkg/overlay/mount.go` (`//go:build linux`)

```go
const lowerdirMaxBytes = 3072 // консервативный лимит (страница ядра ~4096 - запас на остальные опции)

type MountOptions struct {
    LowerDirs []string // bottom-to-top; функция переворачивает для overlayfs
    Target    string
}

type BindOptions struct {
    Source string
    Target string
}

type TmpfsOptions struct {
    Target string
    Size   string // передаётся как "size=<value>" в опциях mount; пустая строка — без ограничения
}

func Mount(opts MountOptions) error
func BindMount(opts BindOptions) error
func MountTmpfs(opts TmpfsOptions) error
func Unmount(target string) error
```

`Mount`:
1. Перевернуть `LowerDirs` → overlayfs ожидает `top:...:bottom`
2. Собрать строку `lowerdir=<top>:...<bottom>,ro`
3. **Если `len(optsStr) > lowerdirMaxBytes` → вернуть ошибку:**
   ```
   lowerdir option string is N bytes, exceeds kernel limit of ~4096 bytes
   (image has too many layers; consider squashing with 'oci-packer copy')
   ```
4. `os.MkdirAll(Target, 0755)`
5. `syscall.Mount("overlay", target, "overlay", syscall.MS_RDONLY, optsStr)`

`BindMount`:
1. `os.MkdirAll(Target, 0755)`
2. `syscall.Mount(Source, Target, "", syscall.MS_BIND, "")`

`MountTmpfs`:
1. `os.MkdirAll(Target, 0755)`
2. Если `opts.Size != ""` → `data = "size=" + opts.Size`; иначе `data = ""`
3. `syscall.Mount("tmpfs", Target, "tmpfs", 0, data)`

`Unmount`: `syscall.Unmount(target, 0)`

#### 4б. unit.go (не зависит от платформы)

**Файл:** `pkg/overlay/unit.go`

```go
type UnitOptions struct {
    Overlay   MountOptions
    Tmpfses   []TmpfsOptions
    Binds     []BindOptions
    UnitDir   string
    SourceRef string
}

// WriteUnits returns the names of all written units (overlay first, then
// tmpfs, then bind) so the caller can pass them to `systemctl enable --now`.
func WriteUnits(opts UnitOptions) (unitNames []string, err error)
```

- Path escaping: заменить каждый символ не из `[A-Za-z0-9:._\\-]` на `\xXX` в стиле systemd,
  `/` → `-`, убрать ведущий `-` (аналог `systemd-escape --path`)
- Записать `<escaped-dst>.mount` для overlay
- Для каждого tmpfs записать `<escaped-tmpfs-target>.mount` с `After=` и `BindsTo=` на overlay unit;
  если `TmpfsOptions.Size != ""` → добавить `Options=size=<value>` в секцию `[Mount]`
- Для каждого bind записать `<escaped-bind-target>.mount` с `After=` и `BindsTo=` на overlay unit
- **Перед записью проверить длину lowerdir-строки** — та же валидация, что в `Mount`
- Вернуть имена всех записанных units в порядке: overlay → tmpfs → bind

#### 4в. mount_unsupported.go

**Файл:** `pkg/overlay/mount_unsupported.go` (`//go:build !linux`)

Заглушки `Mount`, `BindMount`, `MountTmpfs`, `Unmount` возвращают
`errors.New("overlay mount is only supported on Linux")`.

---

### 5. Cobra-команда cmd/umount.go

**Файл:** `cmd/umount.go` (новый)

```go
var umountCmd = &cobra.Command{
    Use:   "umount <dst>",
    Short: "Unmount OCI overlay mount and all associated bind/tmpfs mounts",
    Args:  cobra.ExactArgs(1),
    Run:   umountCmdRun,
}

func init() {
    rootCmd.AddCommand(umountCmd)
    umountCmd.Flags().Bool("lazy", false, "Use lazy unmount (MNT_DETACH) — detach even if busy")
}
```

`umountCmdRun` алгоритм:

```
dst = args[0]

// Читаем /proc/mounts и находим все точки монтирования с префиксом dst,
// сортируем по убыванию глубины пути (дочерние раньше родительских)
mounts = readProcMounts()
targets = filter(mounts, prefix=dst), sort by depth desc

var errs []error
for each target in targets:
    if err := overlay.Unmount(target); err != nil:  // если --lazy: MNT_DETACH
        errs = append(errs, fmt.Errorf("umount %s: %w", target, err))
        log.WithError(err).Warn("failed to unmount, continuing")
    else:
        log.Info("unmounted", target)

if len(errs) > 0:
    // Все ошибки собраны — выводим сводку и выходим с ненулевым кодом
    log.Fatalf("%d mount(s) failed to unmount:\n%s", len(errs), joinErrors(errs))
```

Порядок размонтирования гарантирует, что bind/tmpfs снимаются до overlay;
без этого overlay вернул бы `EBUSY`. Ошибка на одном mount'е не прерывает
размонтирование остальных — все попытки выполняются, итоговый код возврата ненулевой
если хотя бы одна не удалась.

Функция `readProcMounts() []string` — разбирает `/proc/mounts`, возвращает список точек
монтирования (второй столбец). Реализуется в `pkg/overlay/proc.go` (`//go:build linux`).

---

### 6. Константа autoTmpfsPaths

**Файл:** `pkg/overlay/tmpfs.go`

```go
// AutoTmpfsPaths are volatile paths automatically mounted as tmpfs
// to prevent EROFS errors in applications that write to these locations.
var AutoTmpfsPaths = []string{
    "/tmp",
    "/run",
    "/var/tmp",
    "/var/run", // часто симлинк на /run; монтировать только если не симлинк
}
```

Функция `ResolveAutoTmpfs(dst string, sizes map[string]string) []TmpfsOptions` — для каждого пути из списка:
- Проверить, что `filepath.Join(dst, p)` не является симлинком (`os.Lstat`)
- Вернуть `TmpfsOptions{Target: filepath.Join(dst, p), Size: sizes[p]}` — если пути нет в `sizes`, `Size` будет `""` (без ограничения)

---

### 6. Cobra-команда cmd/mount.go

**Файл:** `cmd/mount.go` (новый, по образцу `cmd/copy.go`)

```go
var mountCmd = &cobra.Command{
    Use:   "mount <src> <dst>",
    Short: "Mount OCI layout layers read-only via overlayfs (Linux only)",
    Args:  cobra.ExactArgs(2),
    Run:   mountCmdRun,
}

func init() {
    rootCmd.AddCommand(mountCmd)
    mountCmd.Flags().StringArray("bind",          nil,   "Bind mount: <host-path>:<container-path>")
    mountCmd.Flags().Bool("no-auto-tmpfs",         false, "Disable automatic tmpfs mounts for /tmp, /run, /var/tmp")
    mountCmd.Flags().StringSlice("tmpfs-size",     nil,   "Per-path tmpfs size: <path>:<size>; repeatable and/or comma-separated, e.g. --tmpfs-size /tmp:512m,/run:64m")
    mountCmd.Flags().Bool("verify",                false, "Verify layer digests before mounting (slow for large images)")
    mountCmd.Flags().Bool("persistent",            false, "Write systemd.mount units instead of mounting now")
    mountCmd.Flags().String("unit-dir", "/run/systemd/system", "Directory for systemd unit files")
    mountCmd.Flags().Bool("enable",                false, "Run 'systemctl enable --now' after writing units (requires --persistent)")
    mountCmd.MarkFlagsMutuallyExclusive("enable", "no-auto-tmpfs") // --enable без --persistent бессмысленен, проверяется в run
}
```

`mountCmdRun` алгоритм:

```
reference.Parse(src) → ref
layout.Open(ref) → layoutResolver

if --verify:
    layoutResolver.VerifyLayers(ctx, ref) → error (прерывает выполнение)

layoutResolver.LayerDirs(ctx, ref) → lowerDirs   // включает проверку длины lowerdir
binds     = parseBindFlags(--bind)
sizeMap   = parseTmpfsSizeFlags(--tmpfs-size)  // map[string]string{"/tmp": "512m", "/run": "64m"}
tmpfses   = []
if !--no-auto-tmpfs:
    tmpfses = overlay.ResolveAutoTmpfs(dst, sizeMap)

if --persistent:
    if --enable && --unit-dir == "/run/systemd/system":
        log warn: "--enable with /run/systemd/system won't survive reboot; consider --unit-dir /etc/systemd/system"
    unitNames = overlay.WriteUnits({Overlay: {lowerDirs, dst}, Tmpfses: tmpfses, Binds: binds, UnitDir, SourceRef: src})
    log: "units written to <unit-dir>"
    if --enable:
        // unitNames = [overlay, tmpfs..., bind...] — все units в одном вызове
        exec: systemctl daemon-reload
        exec: systemctl enable --now <unitNames...>
else:
    if --enable:
        fatal: "--enable requires --persistent"
    overlay.Mount({LowerDirs: lowerDirs, Target: dst})
    for each tmpfs: overlay.MountTmpfs(tmpfs)
    for each bind:  overlay.BindMount(bind)
```

---

### 7. Тесты

#### 7а. Layout.LayerDirs (unit)

| Тест | Что проверяет |
|------|---------------|
| `TestLayerDirs_UnpackMode` | Правильные пути, порядок bottom-to-top |
| `TestLayerDirs_TarModeFails` | Ошибка если не unpack-режим |
| `TestLayerDirs_IndexAutoSelectPlatform` | Index с несколькими манифестами → выбирается текущая платформа |

#### 7б. Layout.VerifyLayers (unit)

| Тест | Что проверяет |
|------|---------------|
| `TestVerifyLayers_TarMode_OK` | sha256 blob-файла совпадает с digest из манифеста |
| `TestVerifyLayers_TarMode_Tampered` | Изменённый blob → ошибка с указанием номера слоя |
| `TestVerifyLayers_UnpackMode_OK` | Распакованная директория → re-tar → sha256 совпадает |
| `TestVerifyLayers_UnpackMode_Tampered` | Добавленный файл в директорию → ошибка |

#### 7в. overlay.Mount, BindMount, MountTmpfs (unit, только Linux)

```go
if os.Getuid() != 0 { t.Skip("requires root") }
```

| Тест | Что проверяет |
|------|---------------|
| `TestMount_ReadOnly` | Файлы видны; запись возвращает `EROFS` |
| `TestMount_LowerdirTooLong` | Ошибка до `syscall.Mount` при превышении лимита |
| `TestMountTmpfs_Writable` | tmpfs writable поверх read-only overlay |
| `TestBindMount_Writable` | bind-директория writable |
| `TestUnmount` | После Unmount точка монтирования пуста |

#### 7г. overlay.WriteUnits (unit, любая платформа)

| Тест | Что проверяет |
|------|---------------|
| `TestWriteUnits_OverlayOnly` | `[Mount]` секция: `Type=overlay`, `Options=lowerdir=...,ro` |
| `TestWriteUnits_AutoTmpfs` | Для каждого tmpfs-пути создаётся unit с `After=` и `BindsTo=` |
| `TestWriteUnits_TmpfsSize` | `Options=size=512m` для `/tmp` и `Options=size=64m` для `/run` при per-path размерах; пути без размера не содержат `Options=size=` |
| `TestWriteUnits_WithBinds` | bind unit содержит `After=` и `BindsTo=` на overlay unit |
| `TestWriteUnits_EscapedName` | `/mnt/rootfs` → `mnt-rootfs.mount` |
| `TestWriteUnits_LowerdirTooLong` | Ошибка возвращается до записи файлов |

#### 7д. overlay.Umount (unit, только Linux)

```go
if os.Getuid() != 0 { t.Skip("requires root") }
```

| Тест | Что проверяет |
|------|---------------|
| `TestUmount_UnmountsChildrenFirst` | bind/tmpfs размонтируются до overlay (порядок по глубине пути) |
| `TestUmount_ContinuesOnError` | Ошибка на одном mount'е не прерывает остальные; итоговый код ненулевой |
| `TestUmount_LazyFlag` | `MNT_DETACH` передаётся при `lazy=true` |
| `TestUmount_UnknownTarget` | Нет паники/зависания если dst не смонтирован |

#### 7е. cmd/mount + cmd/umount (интеграционные, только Linux)

- Создать unpack-layout с одним слоем
- Запустить `mountCmdRun`, проверить файлы в dst и writable `/tmp`
- Запустить `umountCmdRun`, проверить что все точки монтирования сняты

---

## Порядок реализации

```
1 (readManifestBlob + resolveManifest)
  ├─ 2 (LayerDirs) + тесты 7а
  └─ 3 (VerifyLayers) + тесты 7б
       ├─ 4а (overlay.Mount/Bind/Tmpfs + proc.go) + тесты 7в
       ├─ 4б (overlay.WriteUnits) + тесты 7г
       ├─ 5 (umount cmd) + тесты 7д
       └─ 6 (AutoTmpfsPaths)
            └─ 7 (cmd/mount.go) + тест 7е
               └─ (cmd/umount.go уже готов из шага 5)
```

Шаги 2, 3 независимы после шага 1.
Шаги 4а, 4б, 5, 6 независимы после шагов 2–3 — можно делать параллельно.

---

## Минусы и потенциальные проблемы

### Требования к привилегиям
- `mount -t overlay` требует `CAP_SYS_ADMIN` (фактически — root).
  Без него `syscall.Mount` возвращает `EPERM`.
  Непривилегированный режим не поддерживается.

### Зависимость mount от расположения oci-layout
- `lowerdir` прописывается как абсолютный путь к директориям внутри oci-layout.
  Если layout переместить, переименовать или удалить — смонтированная ФС начнёт
  возвращать ошибки ввода-вывода для всех файлов без каких-либо предупреждений.
  В отличие от containerd/Docker, которые копируют слои в постоянное хранилище,
  здесь layout остаётся единственным источником данных.
- systemd unit хранит жёсткие пути: при переезде layout нужно перегенерировать units
  и сделать `systemctl daemon-reload`.

### Лимит длины lowerdir ✓ обрабатывается
- Строка опций mount ограничена размером страницы ядра (~4096 байт).
  Образы с большим числом слоёв упрутся в этот лимит.
  `overlay.Mount` и `WriteUnits` проверяют длину **до** вызова ядра и возвращают
  понятную ошибку с подсказкой использовать `oci-packer copy` для объединения слоёв.

### Совместимость файловых систем
- overlayfs не поддерживает в качестве lowerdir некоторые типы ФС: NFS, FUSE, CIFS,
  FAT/exFAT, ещё один overlayfs без флага `index=on`.
  Если oci-layout лежит на несовместимой ФС, `syscall.Mount` упадёт с `EINVAL`.

### Порядок размонтирования ✓ обрабатывается
- `oci-packer umount` читает `/proc/mounts`, сортирует точки монтирования по убыванию
  глубины пути и снимает их по порядку — bind/tmpfs всегда размонтируются до overlay.
  Флаг `--lazy` (`MNT_DETACH`) позволяет отсоединить занятые mount'ы.
  При `--persistent` systemd снимает эту проблему через `BindsTo=`.

### Read-only ломает приложения ✓ частично обрабатывается
- Стандартные volatile-пути (`/tmp`, `/run`, `/var/tmp`) монтируются как tmpfs автоматически;
  размер задаётся per-path через `--tmpfs-size /tmp:512m --tmpfs-size /run:64m`; пути без
  явного размера монтируются без ограничения (по умолчанию ядро даёт 50% RAM).
  Нестандартные пути (специфичные для конкретного приложения) нужно задавать через `--bind`
  вручную — нет автоматического определения из `config.json` образа.

### Верификация слоёв в unpack-режиме ✓ обрабатывается
- `--verify` реализован, но для unpack-режима требует re-tar + сжатие каждого слоя —
  сложность O(суммарный размер образа). Для образов >1 ГБ это заметно медленно.
  Предупреждение выводится в лог перед запуском верификации.

### Нет интеграции с пространствами имён
- Команда делает только filesystem mount; она не создаёт network namespace, pid namespace,
  cgroup и т.д. Использование `oci-packer mount` как замены полноценного container runtime
  требует дополнительной оркестрации снаружи.

### systemd unit и /run vs /etc
- По умолчанию units пишутся в `/run/systemd/system` — они исчезают после перезагрузки.
  Для постоянного монтирования нужно `--unit-dir /etc/systemd/system`, а oci-layout должен
  быть доступен по тем же путям после загрузки.
  `systemctl daemon-reload` вызывается вручную; `--enable` явно активирует все сгенерированные
  units (overlay + tmpfs + bind) одним вызовом `systemctl enable --now`.

---

## Нерешённые вопросы

_Открытых вопросов нет — все решения зафиксированы выше._
