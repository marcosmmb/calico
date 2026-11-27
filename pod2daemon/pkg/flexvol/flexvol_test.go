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

package flexvol

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	idleMountInfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
300 22 0:50 / /var/lib/kubelet/pods/11111111-2222-3333-4444-555555555555/volumes/kubernetes.io~projected/kube-api-access rw,relatime shared:90 - tmpfs tmpfs rw
301 22 0:51 / /var/lib/kubelet/pods/11111111-2222-3333-4444-555555555555/volumes/kubernetes.io~csi/policysync/mount rw,relatime shared:91 - tmpfs tmpfs rw
`
	inUseMountInfo = idleMountInfo +
		`310 22 0:60 / /var/lib/kubelet/pods/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/volumes/nodeagent~uds/policysync rw,relatime shared:95 - tmpfs tmpfs rw,size=8k
311 310 0:25 /nodeagent/mount/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee /var/lib/kubelet/pods/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/volumes/nodeagent~uds/policysync/nodeagent rw,nosuid shared:5 - tmpfs tmpfs rw
`
)

// installedDriver creates a plugin directory holding the driver as an earlier
// release would have left it, and returns the driver directory.
func installedDriver(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), driverDirName)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{driverBinary, tmpBinaryPrefix + "123456"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("driver"), 0o550); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func writeMountInfo(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write mountinfo: %v", err)
	}
	return path
}

func TestUninstallDriverRemovesDriverDir(t *testing.T) {
	dir := installedDriver(t)

	if err := uninstallDriver(dir, writeMountInfo(t, idleMountInfo)); err != nil {
		t.Fatalf("uninstallDriver: %v", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("driver dir still present, lstat err = %v", err)
	}
}

func TestUninstallDriverLeavesDriverWhileMounted(t *testing.T) {
	dir := installedDriver(t)

	if err := uninstallDriver(dir, writeMountInfo(t, inUseMountInfo)); err != nil {
		t.Fatalf("uninstallDriver: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, driverBinary)); err != nil {
		t.Fatalf("driver was removed while a pod still mounts it: %v", err)
	}
}

func TestUninstallDriverMissingDriverIsNoOp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), driverDirName)

	if err := uninstallDriver(dir, writeMountInfo(t, idleMountInfo)); err != nil {
		t.Fatalf("uninstallDriver: %v", err)
	}
}

func TestUninstallDriverKeepsForeignFiles(t *testing.T) {
	dir := installedDriver(t)
	foreign := filepath.Join(dir, "other")
	if err := os.WriteFile(foreign, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := uninstallDriver(dir, writeMountInfo(t, idleMountInfo)); err != nil {
		t.Fatalf("uninstallDriver: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, driverBinary)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("driver still present, lstat err = %v", err)
	}
}

func TestUninstallDriverRejectsBadArguments(t *testing.T) {
	mountInfo := writeMountInfo(t, idleMountInfo)
	for name, tc := range map[string]struct{ target, mountInfo string }{
		"empty target":        {"", mountInfo},
		"empty mountinfo":     {filepath.Join(t.TempDir(), driverDirName), ""},
		"unexpected basename": {t.TempDir(), mountInfo},
	} {
		t.Run(name, func(t *testing.T) {
			if err := uninstallDriver(tc.target, tc.mountInfo); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestUninstallDriverRefusesSymlink(t *testing.T) {
	real := t.TempDir()
	victim := filepath.Join(real, driverBinary)
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(t.TempDir(), driverDirName)
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := uninstallDriver(link, writeMountInfo(t, idleMountInfo)); err == nil {
		t.Fatal("expected an error for a symlinked driver dir")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file behind the symlink was removed: %v", err)
	}
}

// Without a readable mount table there is no way to tell whether the driver is
// in use, so it must be left alone.
func TestUninstallDriverUnreadableMountInfoKeepsDriver(t *testing.T) {
	dir := installedDriver(t)

	if err := uninstallDriver(dir, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error when mountinfo cannot be read")
	}
	if _, err := os.Stat(filepath.Join(dir, driverBinary)); err != nil {
		t.Fatalf("driver was removed without a mount check: %v", err)
	}
}

func TestParseDriverMounts(t *testing.T) {
	got, err := parseDriverMounts(strings.NewReader(inUseMountInfo))
	if err != nil {
		t.Fatalf("parseDriverMounts: %v", err)
	}
	want := []string{"/var/lib/kubelet/pods/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/volumes/nodeagent~uds/policysync"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts = %v, want %v", got, want)
	}

	got, err = parseDriverMounts(strings.NewReader(idleMountInfo))
	if err != nil {
		t.Fatalf("parseDriverMounts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("mounts = %v, want none", got)
	}
}
