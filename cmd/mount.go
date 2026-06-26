//go:build linux

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

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/arenadata/oci-packer/internal/logger"
	"github.com/arenadata/oci-packer/pkg/overlay"
	"github.com/arenadata/oci-packer/pkg/registry/oci-layout"
	"github.com/arenadata/oci-packer/pkg/registry/reference"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const defaultUnitDir = "/run/systemd/system"

type logEntry = *logrus.Entry

var mountCmd = &cobra.Command{
	Use:   "mount <oci://layout:repo:tag> <dst>",
	Short: "Mount an image from an OCI layout read-only via overlayfs (Linux only)",
	Long: `Mount the layers of a container image from an unpacked OCI layout read-only
at <dst> using overlayfs.

A layout may contain many images, so <src> selects one image inside it:

    oci://<layout-dir>:<repository>:<tag>
    e.g. oci://./layout:example/service:v1

Only container images can be mounted; arbitrary OCI artifacts are rejected.

Volatile paths (/tmp, /run, /var/tmp) are mounted as writable tmpfs by default.
Use --bind to expose additional writable host directories, and --persistent to
generate systemd.mount units instead of mounting immediately.`,
	Args: cobra.ExactArgs(2),
	Run:  mountCmdRun,
}

func init() {
	rootCmd.AddCommand(mountCmd)

	mountCmd.Flags().StringArray("bind", nil, "Bind mount: <host-path>:<container-path> (repeatable)")
	mountCmd.Flags().Bool("no-auto-tmpfs", false, "Disable automatic tmpfs mounts for /tmp, /run, /var/tmp")
	mountCmd.Flags().StringSlice("tmpfs-size", nil, "Per-path tmpfs size: <path>:<size>, e.g. /tmp:512m,/run:64m")
	mountCmd.Flags().Bool("verify", false, "Verify layer digests before mounting (tar-mode layouts only)")
	mountCmd.Flags().Bool("persistent", false, "Write systemd.mount units instead of mounting now")
	mountCmd.Flags().String("unit-dir", defaultUnitDir, "Directory for systemd unit files")
	mountCmd.Flags().Bool("enable", false, "Run 'systemctl enable --now' after writing units (requires --persistent)")
}

func mountCmdRun(cmd *cobra.Command, args []string) {
	src, dst := args[0], args[1]
	log := logger.New("mount")

	persistent, _ := cmd.Flags().GetBool("persistent")
	enable, _ := cmd.Flags().GetBool("enable")
	verify, _ := cmd.Flags().GetBool("verify")
	noAutoTmpfs, _ := cmd.Flags().GetBool("no-auto-tmpfs")
	unitDir, _ := cmd.Flags().GetString("unit-dir")
	bindSpecs, _ := cmd.Flags().GetStringArray("bind")
	sizeSpecs, _ := cmd.Flags().GetStringSlice("tmpfs-size")

	if enable && !persistent {
		log.Fatal("--enable requires --persistent")
	}

	parsedRef, err := reference.Parse(src)
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to parse source reference")
	}

	resolver, err := layout.Open(parsedRef)
	if err != nil {
		log.WithError(err).WithField("src", src).Fatal("failed to open OCI layout")
	}
	l, ok := resolver.(*layout.Layout)
	if !ok {
		log.Fatal("source is not an OCI layout")
	}

	if verify {
		log.Info("verifying layer digests")
		if err = l.VerifyLayers(cmd.Context(), reference.Reference{}); err != nil {
			log.WithError(err).Fatal("layer verification failed")
		}
		log.Info("all layers verified")
	}

	lowerDirs, err := l.LayerDirs(cmd.Context(), reference.Reference{})
	if err != nil {
		log.WithError(err).Fatal("failed to resolve layer directories")
	}

	binds, err := parseBinds(dst, bindSpecs)
	if err != nil {
		log.WithError(err).Fatal("invalid --bind")
	}

	sizes, err := parseTmpfsSizes(sizeSpecs)
	if err != nil {
		log.WithError(err).Fatal("invalid --tmpfs-size")
	}

	mountOpts := overlay.MountOptions{LowerDirs: lowerDirs, Target: dst}

	if persistent {
		var tmpfses []overlay.TmpfsOptions
		if !noAutoTmpfs {
			tmpfses = overlay.ResolveAutoTmpfs(dst, sizes)
		}
		mountPersistent(log, mountOpts, tmpfses, binds, unitDir, src, enable)
		return
	}

	mountNow(log, mountOpts, binds, dst, noAutoTmpfs, sizes)
}

func mountNow(log logEntry, mountOpts overlay.MountOptions, binds []overlay.BindOptions,
	dst string, noAutoTmpfs bool, sizes map[string]string) {

	if err := overlay.Mount(mountOpts); err != nil {
		log.WithError(err).Fatal("overlay mount failed")
	}
	log.WithField("target", dst).Info("overlay mounted read-only")

	// Resolve auto-tmpfs only now that the overlay is mounted, so symlinks in
	// the image (e.g. /var/run -> /run) resolve against it and are caught by
	// EnsureWithin instead of landing a tmpfs on the host's real /run.
	if !noAutoTmpfs {
		for _, tm := range overlay.ResolveAutoTmpfs(dst, sizes) {
			if err := overlay.EnsureWithin(dst, tm.Target); err != nil {
				log.WithError(err).WithField("target", tm.Target).Warn("skipping tmpfs: target escapes overlay")
				continue
			}
			if err := overlay.MountTmpfs(tm); err != nil {
				log.WithError(err).WithField("target", tm.Target).Fatal("tmpfs mount failed")
			}
			log.WithField("target", tm.Target).Debug("tmpfs mounted")
		}
	}

	for _, b := range binds {
		if err := overlay.EnsureWithin(dst, b.Target); err != nil {
			log.WithError(err).WithField("target", b.Target).Fatal("bind target escapes overlay")
		}
		if err := overlay.BindMount(b); err != nil {
			log.WithError(err).WithField("target", b.Target).Fatal("bind mount failed")
		}
		log.WithFields(map[string]any{"source": b.Source, "target": b.Target}).Info("bind mounted")
	}

	log.WithField("target", dst).Info("mount completed")
}

func mountPersistent(log logEntry, mountOpts overlay.MountOptions, tmpfses []overlay.TmpfsOptions,
	binds []overlay.BindOptions, unitDir, src string, enable bool) {

	if enable && unitDir == defaultUnitDir {
		log.Warn("--enable with /run/systemd/system won't survive reboot; " +
			"consider --unit-dir /etc/systemd/system")
	}

	unitNames, err := overlay.WriteUnits(overlay.UnitOptions{
		Overlay:   mountOpts,
		Tmpfses:   tmpfses,
		Binds:     binds,
		UnitDir:   unitDir,
		SourceRef: src,
	})
	if err != nil {
		log.WithError(err).Fatal("failed to write systemd units")
	}
	log.WithFields(map[string]any{"unit_dir": unitDir, "units": len(unitNames)}).Info("systemd units written")

	if !enable {
		log.Infof("run: systemctl daemon-reload && systemctl start %s", strings.Join(unitNames, " "))
		return
	}

	if err = runSystemctl("daemon-reload"); err != nil {
		log.WithError(err).Fatal("systemctl daemon-reload failed")
	}
	if err = runSystemctl(append([]string{"enable", "--now"}, unitNames...)...); err != nil {
		log.WithError(err).Fatal("systemctl enable --now failed")
	}
	log.Info("units enabled and started")
}

// parseBinds turns "<host>:<container>" specs into BindOptions rooted at dst.
func parseBinds(dst string, specs []string) ([]overlay.BindOptions, error) {
	var binds []overlay.BindOptions
	for _, s := range specs {
		host, container, ok := strings.Cut(s, ":")
		if !ok || host == "" || container == "" {
			return nil, fmt.Errorf("invalid bind %q, want <host-path>:<container-path>", s)
		}
		binds = append(binds, overlay.BindOptions{
			Source: host,
			Target: filepath.Join(dst, container),
		})
	}
	return binds, nil
}

// parseTmpfsSizes turns "<path>:<size>" specs into a path-keyed size map.
func parseTmpfsSizes(specs []string) (map[string]string, error) {
	sizes := make(map[string]string, len(specs))
	for _, s := range specs {
		i := strings.LastIndex(s, ":")
		if i <= 0 || i == len(s)-1 {
			return nil, fmt.Errorf("invalid tmpfs-size %q, want <path>:<size>", s)
		}
		sizes[s[:i]] = s[i+1:]
	}
	return sizes, nil
}

func runSystemctl(args ...string) error {
	c := exec.Command("systemctl", args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
