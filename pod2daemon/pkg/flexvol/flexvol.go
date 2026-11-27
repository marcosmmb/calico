// Copyright (c) 2026 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package flexvol removes the nodeagent/uds FlexVolume driver that earlier
// releases installed on the host. Calico no longer ships that driver; pods get
// their policy sync socket from the CSI driver instead.
package flexvol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	// driverDirName is kubelet's on-disk name for the nodeagent/uds driver,
	// both in its plugin directory and in each pod's volumes directory.
	driverDirName = "nodeagent~uds"
	driverBinary  = "uds"
	// tmpBinaryPrefix matches the temp files the installer wrote next to the
	// driver before renaming them into place.
	tmpBinaryPrefix = ".uds."
)

// NewCommand returns the root cobra command for FlexVolume driver cleanup.
func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "flexvol",
		Short:         "Clean up the legacy FlexVolume driver.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	var target, mountInfo string
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the FlexVolume driver from the host filesystem.",
		Long: "Uninstall removes the nodeagent~uds driver directory given by --target. " +
			"The driver is left in place while any pod on the host still has one of its " +
			"volumes mounted, because kubelet needs the driver to unmount them.",
		RunE: func(c *cobra.Command, args []string) error {
			return uninstallDriver(target, mountInfo)
		},
	}
	uninstallCmd.Flags().StringVar(&target, "target", "", "Path to the driver directory on the host (e.g. /host/driver/nodeagent~uds).")
	uninstallCmd.Flags().StringVar(&mountInfo, "mountinfo", "", "Path to the host's mountinfo file (e.g. /nodeproc/1/mountinfo).")

	rootCmd.AddCommand(uninstallCmd)
	return rootCmd
}

func uninstallDriver(target, mountInfoPath string) error {
	if target == "" {
		return fmt.Errorf("target is required")
	}
	if mountInfoPath == "" {
		return fmt.Errorf("mountinfo is required")
	}
	if base := filepath.Base(target); base != driverDirName {
		return fmt.Errorf("target basename must be %q, got %q", driverDirName, base)
	}

	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", target)
	}

	inUse, err := mountedDriverVolumes(mountInfoPath)
	if err != nil {
		return fmt.Errorf("checking for mounted FlexVolume volumes: %w", err)
	}
	if len(inUse) > 0 {
		logrus.WithField("mounts", inUse).Warn(
			"Leaving the deprecated FlexVolume driver installed because pods on this node still mount it. " +
				"Migrate those pods to the csi.tigera.io CSI driver; the driver is removed the next time calico-node starts without them.")
		return nil
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != driverBinary && !strings.HasPrefix(e.Name(), tmpBinaryPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(target, e.Name())); err != nil {
			return err
		}
	}

	// Anything else in the directory was not put there by Calico, so the
	// directory is only removed if it is now empty.
	if err := os.Remove(target); err != nil {
		if remaining, readErr := os.ReadDir(target); readErr == nil && len(remaining) > 0 {
			logrus.WithField("dir", target).Warn("Removed the FlexVolume driver but left its directory, which holds other files.")
			return nil
		}
		return err
	}
	logrus.WithField("dir", target).Info("Removed the FlexVolume driver.")
	return nil
}

// mountedDriverVolumes returns the mount points of nodeagent/uds volumes
// listed in the given mountinfo file.
func mountedDriverVolumes(mountInfoPath string) ([]string, error) {
	f, err := os.Open(mountInfoPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseDriverMounts(f)
}

func parseDriverMounts(r io.Reader) ([]string, error) {
	// Kubelet mounts a pod's volume at <root>/pods/<uid>/volumes/<driver>/<name>.
	marker := "/volumes/" + driverDirName + "/"

	var mounts []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// The mount point is the fifth field of a mountinfo line.
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		idx := strings.Index(fields[4], marker)
		if idx < 0 {
			continue
		}
		// The driver bind-mounts a subdirectory inside each volume; report
		// the volume once.
		volume := fields[4]
		if end := strings.Index(volume[idx+len(marker):], "/"); end >= 0 {
			volume = volume[:idx+len(marker)+end]
		}
		if !seen[volume] {
			seen[volume] = true
			mounts = append(mounts, volume)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}
