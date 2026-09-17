package main

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 14.5: the release script produces a binary per architecture, each of them
// static and each of them for the machine it claims.
//
// Running one is the real check and it only works where the architecture is
// the machine's own or emulated, so this runs what it can and inspects the
// rest. A binary that is the wrong architecture, or that grew a dynamic
// dependency on the build machine's libc, fails here rather than on somebody's
// NAS.
func TestCrossCompiledArtifacts(t *testing.T) {
	out := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "sh", "scripts/release.sh")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "OUT="+out, "SKIP_WEB=1", "VERSION=v0.0.0-test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/release.sh: %v\n%s", err, output)
	}

	for _, tc := range []struct {
		arch    string
		machine elf.Machine
	}{
		{"amd64", elf.EM_X86_64},
		{"arm64", elf.EM_AARCH64},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			path := filepath.Join(out, "drive-linux-"+tc.arch)
			f, err := elf.Open(path)
			if err != nil {
				t.Fatalf("not an artifact this machine can even read: %v", err)
			}
			defer f.Close()
			if f.Machine != tc.machine {
				t.Errorf("built for %s, want %s", f.Machine, tc.machine)
			}
			// An interpreter section means it needs a dynamic loader, which is
			// the one thing a single-file release must not ask for.
			if f.Section(".interp") != nil {
				t.Error("dynamically linked: something pulled in cgo")
			}

			argv := append(runnerFor(t, tc.arch), path, "version")
			output, err := exec.CommandContext(t.Context(), argv[0], argv[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("%s version: %v\n%s", path, err, output)
			}
			if strings.TrimSpace(string(output)) != "v0.0.0-test" {
				t.Errorf("printed %q, want the version it was built with", output)
			}
		})
	}
}

// runnerFor returns the command that can execute a binary for arch, skipping
// when this machine has no way to.
func runnerFor(t *testing.T, arch string) []string {
	t.Helper()
	if arch == runtime.GOARCH {
		return []string{}
	}
	for _, qemu := range []string{"qemu-" + qemuName(arch) + "-static", "qemu-" + qemuName(arch)} {
		if path, err := exec.LookPath(qemu); err == nil {
			return []string{path}
		}
	}
	// binfmt_misc would let the kernel do this transparently; without it or
	// qemu installed, the artifact can be inspected but not started here.
	t.Skipf("no way to run a %s binary on %s: install qemu-user-static to check this one by running it", arch, runtime.GOARCH)
	return nil
}

func qemuName(arch string) string {
	if arch == "arm64" {
		return "aarch64"
	}
	return arch
}
