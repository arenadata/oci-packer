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

package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnitOptions bundles everything needed to generate the systemd .mount units
// for a read-only overlay plus its tmpfs and bind mounts.
type UnitOptions struct {
	Overlay   MountOptions
	Tmpfses   []TmpfsOptions
	Binds     []BindOptions
	UnitDir   string
	SourceRef string
}

// WriteUnits writes a systemd .mount unit for the overlay and for each tmpfs
// and bind mount into opts.UnitDir. It returns the unit names in dependency
// order (overlay first, then tmpfs, then bind) so the caller can pass them to
// `systemctl enable --now`.
func WriteUnits(opts UnitOptions) ([]string, error) {
	overlayOpts, err := overlayOptions(opts.Overlay.LowerDirs)
	if err != nil {
		return nil, err
	}

	if err = os.MkdirAll(opts.UnitDir, 0755); err != nil {
		return nil, err
	}

	overlayUnit := unitName(opts.Overlay.Target)
	names := []string{overlayUnit}

	overlayBody := buildMountUnit(mountUnit{
		Description: fmt.Sprintf("OCI overlay mount: %s -> %s", opts.SourceRef, opts.Overlay.Target),
		What:        "overlay",
		Where:       opts.Overlay.Target,
		Type:        "overlay",
		Options:     overlayOpts,
	})
	if err = writeUnitFile(opts.UnitDir, overlayUnit, overlayBody); err != nil {
		return nil, err
	}

	for _, tm := range opts.Tmpfses {
		name := unitName(tm.Target)
		var options string
		if tm.Size != "" {
			options = "size=" + tm.Size
		}
		body := buildMountUnit(mountUnit{
			Description: fmt.Sprintf("tmpfs on %s", tm.Target),
			After:       overlayUnit,
			BindsTo:     overlayUnit,
			What:        "tmpfs",
			Where:       tm.Target,
			Type:        "tmpfs",
			Options:     options,
		})
		if err = writeUnitFile(opts.UnitDir, name, body); err != nil {
			return nil, err
		}
		names = append(names, name)
	}

	for _, b := range opts.Binds {
		name := unitName(b.Target)
		body := buildMountUnit(mountUnit{
			Description: fmt.Sprintf("Bind mount %s -> %s", b.Source, b.Target),
			After:       overlayUnit,
			BindsTo:     overlayUnit,
			What:        b.Source,
			Where:       b.Target,
			Type:        "none",
			Options:     "bind",
		})
		if err = writeUnitFile(opts.UnitDir, name, body); err != nil {
			return nil, err
		}
		names = append(names, name)
	}

	return names, nil
}

type mountUnit struct {
	Description string
	After       string
	BindsTo     string
	What        string
	Where       string
	Type        string
	Options     string
}

func buildMountUnit(u mountUnit) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", u.Description)
	if u.After != "" {
		fmt.Fprintf(&b, "After=%s\n", u.After)
	}
	if u.BindsTo != "" {
		fmt.Fprintf(&b, "BindsTo=%s\n", u.BindsTo)
	}
	b.WriteString("\n[Mount]\n")
	fmt.Fprintf(&b, "What=%s\n", u.What)
	fmt.Fprintf(&b, "Where=%s\n", u.Where)
	fmt.Fprintf(&b, "Type=%s\n", u.Type)
	if u.Options != "" {
		fmt.Fprintf(&b, "Options=%s\n", u.Options)
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

func writeUnitFile(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0644)
}

// unitName converts an absolute mount target into a systemd .mount unit name,
// matching `systemd-escape --path --suffix=mount`.
func unitName(target string) string {
	return escapePath(target) + ".mount"
}

// escapePath mirrors systemd's path escaping: '/' becomes '-', a leading '.'
// and every character outside [A-Za-z0-9_.] (including '-') becomes \xNN.
func escapePath(p string) string {
	p = filepath.Clean(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return "-" // root
	}

	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/':
			b.WriteByte('-')
		case c == '.' && i == 0:
			b.WriteString(`\x2e`)
		case c == '_' || c == '.' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String()
}
