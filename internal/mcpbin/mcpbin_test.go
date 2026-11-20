package mcpbin_test

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/mcpbin"
)

func TestAnUnknownArchitectureIsRefused(t *testing.T) {
	for _, a := range []string{"", "386", "x86_64", "../amd64", "arm64/../amd64"} {
		if b, err := mcpbin.Binary(a); err == nil || errors.Is(err, mcpbin.ErrNotIncluded) || b != nil {
			t.Errorf("%q: %v", a, err)
		}
	}
}

func TestChecksumIsTheSha256InHex(t *testing.T) {
	// SHA-256 of "abc"
	if got := mcpbin.Checksum([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("%s", got)
	}
}

// With VIPER_REQUIRE_MCP=1 (make test) the binaries must be there; otherwise a plain go test works
// without them, as a viper built with go build does.
func TestTheBinariesAreStaticLinuxProgramsOfTheRightArchitecture(t *testing.T) {
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}
	for _, arch := range mcpbin.Architectures() {
		b, err := mcpbin.Binary(arch)
		if errors.Is(err, mcpbin.ErrNotIncluded) {
			if os.Getenv("VIPER_REQUIRE_MCP") == "1" {
				t.Errorf("%s: not included, but VIPER_REQUIRE_MCP=1 (run make mcp)", arch)
			} else {
				t.Logf("%s: not included in this build", arch)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		f, err := elf.NewFile(bytes.NewReader(b))
		if err != nil {
			t.Errorf("%s: no ELF file: %v", arch, err)
			continue
		}
		if f.Machine != want[arch] || f.Class != elf.ELFCLASS64 || f.Type != elf.ET_EXEC {
			t.Errorf("%s: machine %v class %v type %v", arch, f.Machine, f.Class, f.Type)
		}
		// static: no interpreter and no shared libraries
		if f.Section(".interp") != nil {
			t.Errorf("%s: dynamically linked", arch)
		}
		if libs, _ := f.ImportedLibraries(); len(libs) > 0 {
			t.Errorf("%s: needs %v", arch, libs)
		}
	}
}

func TestTheFileNameOfASystemEndsInExeForWindows(t *testing.T) {
	for _, c := range [][3]string{{"linux", "amd64", "viper-nats-mcp_linux_amd64"}, {"darwin", "arm64", "viper-nats-mcp_darwin_arm64"}, {"windows", "amd64", "viper-nats-mcp_windows_amd64.exe"}} {
		if got := mcpbin.HostFileName(c[0], c[1]); got != c[2] {
			t.Errorf("%s/%s: %s", c[0], c[1], got)
		}
	}
	if mcpbin.HostFileName("linux", "arm64") != mcpbin.FileName("arm64") {
		t.Error("a Linux file has one name in both functions")
	}
}

// The program for this computer is of the format and the CPU of this computer (make mcp-host; VIPER_REQUIRE_MCP_HOST=1 insists on it where only the program of this system is built).
func TestTheProgramOfThisComputerIsOfItsFormatAndCPU(t *testing.T) {
	b, err := mcpbin.Host()
	if errors.Is(err, mcpbin.ErrNotIncluded) {
		if os.Getenv("VIPER_REQUIRE_MCP") == "1" || os.Getenv("VIPER_REQUIRE_MCP_HOST") == "1" {
			t.Fatalf("%s/%s: not included, but VIPER_REQUIRE_MCP(_HOST)=1 (run make mcp-host)", runtime.GOOS, runtime.GOARCH)
		}
		t.Skip("not included in this build")
	}
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(b))
		want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
		if err != nil || f.Machine != want {
			t.Errorf("ELF: %v %v", err, f)
		}
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(b))
		want := map[string]macho.Cpu{"amd64": macho.CpuAmd64, "arm64": macho.CpuArm64}[runtime.GOARCH]
		if err != nil || f.Cpu != want {
			t.Errorf("Mach-O: %v %v", err, f)
		}
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(b))
		want := map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64}[runtime.GOARCH]
		if err != nil || f.Machine != want {
			t.Errorf("PE: %v %v", err, f)
		}
	default:
		t.Skip("no check for this system")
	}
}
