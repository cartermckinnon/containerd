// +build windows

/*
Copyright The containerd Authors.

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

package opts

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	osinterface "github.com/containerd/cri/pkg/os"
)

// WithWindowsNetworkNamespace sets windows network namespace for container.
// TODO(windows): Move this into container/containerd.
func WithWindowsNetworkNamespace(path string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		if s.Windows.Network == nil {
			s.Windows.Network = &runtimespec.WindowsNetwork{}
		}
		s.Windows.Network.NetworkNamespace = path
		return nil
	}
}

// WithWindowsMounts sorts and adds runtime and CRI mounts to the spec for
// windows container.
func WithWindowsMounts(osi osinterface.OS, config *runtime.ContainerConfig, extra []*runtime.Mount) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		// mergeMounts merge CRI mounts with extra mounts. If a mount destination
		// is mounted by both a CRI mount and an extra mount, the CRI mount will
		// be kept.
		var (
			criMounts = config.GetMounts()
			mounts    = append([]*runtime.Mount{}, criMounts...)
		)
		// Copy all mounts from extra mounts, except for mounts overriden by CRI.
		for _, e := range extra {
			found := false
			for _, c := range criMounts {
				if filepath.Clean(e.ContainerPath) == filepath.Clean(c.ContainerPath) {
					found = true
					break
				}
			}
			if !found {
				mounts = append(mounts, e)
			}
		}

		// Sort mounts in number of parts. This ensures that high level mounts don't
		// shadow other mounts.
		sort.Sort(orderedMounts(mounts))

		// Copy all mounts from default mounts, except for
		// - mounts overriden by supplied mount;
		// - all mounts under /dev if a supplied /dev is present.
		mountSet := make(map[string]struct{})
		for _, m := range mounts {
			mountSet[filepath.Clean(m.ContainerPath)] = struct{}{}
		}

		defaultMounts := s.Mounts
		s.Mounts = nil

		for _, m := range defaultMounts {
			dst := filepath.Clean(m.Destination)
			if _, ok := mountSet[dst]; ok {
				// filter out mount overridden by a supplied mount
				continue
			}
			s.Mounts = append(s.Mounts, m)
		}

		for _, mount := range mounts {
			var (
				dst = mount.GetContainerPath()
				src = mount.GetHostPath()
			)
			// TODO(windows): Support special mount sources, e.g. named pipe.
			// Create the host path if it doesn't exist.
			if _, err := osi.Stat(src); err != nil {
				// If the source doesn't exist, return an error instead
				// of creating the source. This aligns with Docker's
				// behavior on windows.
				return errors.Wrapf(err, "failed to stat %q", src)
			}
			src, err := osi.ResolveSymbolicLink(src)
			if err != nil {
				return errors.Wrapf(err, "failed to resolve symlink %q", src)
			}

			var options []string
			// NOTE(random-liu): we don't change all mounts to `ro` when root filesystem
			// is readonly. This is different from docker's behavior, but make more sense.
			if mount.GetReadonly() {
				options = append(options, "ro")
			} else {
				options = append(options, "rw")
			}
			s.Mounts = append(s.Mounts, runtimespec.Mount{
				Source:      src,
				Destination: dst,
				Options:     options,
			})
		}
		return nil
	}
}

// WithWindowsResources sets the provided resource restrictions for windows.
func WithWindowsResources(resources *runtime.WindowsContainerResources) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if resources == nil {
			return nil
		}
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		if s.Windows.Resources == nil {
			s.Windows.Resources = &runtimespec.WindowsResources{}
		}
		if s.Windows.Resources.CPU == nil {
			s.Windows.Resources.CPU = &runtimespec.WindowsCPUResources{}
		}
		if s.Windows.Resources.Memory == nil {
			s.Windows.Resources.Memory = &runtimespec.WindowsMemoryResources{}
		}

		var (
			count  = uint64(resources.GetCpuCount())
			shares = uint16(resources.GetCpuShares())
			max    = uint16(resources.GetCpuMaximum())
			limit  = uint64(resources.GetMemoryLimitInBytes())
		)
		if count != 0 {
			s.Windows.Resources.CPU.Count = &count
		}
		if shares != 0 {
			s.Windows.Resources.CPU.Shares = &shares
		}
		if max != 0 {
			s.Windows.Resources.CPU.Maximum = &max
		}
		if limit != 0 {
			s.Windows.Resources.Memory.Limit = &limit
		}
		return nil
	}
}
