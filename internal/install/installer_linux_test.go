package install

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Exercise the actual piped installer, but replace downloads, unpacking, and
// the installed binary with local stubs. Nothing touches the real user service
// or panel, and no network requests are made.
func TestInstallerIntegrationPrompt(t *testing.T) {
	script, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                       string
		terminal                   bool
		replies                    []string
		installExit, integrateExit string
		legacyRelease              bool
		wantIntegrate, wantFail    bool
	}{
		{name: "enter accepts by default", terminal: true, replies: []string{"\n"}, wantIntegrate: true},
		{name: "explicit yes", terminal: true, replies: []string{"YES\n"}, wantIntegrate: true},
		{name: "no leaves panel alone", terminal: true, replies: []string{"n\n"}},
		{name: "invalid answer asks again", terminal: true, replies: []string{"maybe\n", "No\n"}},
		{name: "end of terminal input skips", terminal: true, replies: []string{"\x04"}},
		{name: "no terminal skips"},
		{name: "integration failure keeps installation", terminal: true, replies: []string{"y\n"}, integrateExit: "1", wantIntegrate: true},
		{name: "daemon failure stops before prompt", terminal: true, installExit: "1", wantFail: true},
		{name: "legacy release cannot integrate without consent", terminal: true, legacyRelease: true, wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin, target := filepath.Join(root, "bin"), filepath.Join(root, "installed")
			log := filepath.Join(root, "commands")
			writeTestFile(t, filepath.Join(bin, "uname"), "#!/bin/sh\ncase $1 in -s) echo Linux ;; -m) echo x86_64 ;; esac\n", 0o755)
			writeTestFile(t, filepath.Join(bin, "curl"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; output=$1; fi
  shift
done
printf 'hash omabat_0.1.0_linux_amd64.tar.gz\n' > "$output"
`, 0o755)
			writeTestFile(t, filepath.Join(bin, "sha256sum"), "#!/bin/sh\ncat >/dev/null\n", 0o755)
			writeTestFile(t, filepath.Join(bin, "tar"), `#!/bin/sh
dir="$4/omabat_0.1.0_linux_amd64"
mkdir -p "$dir"
cat > "$dir/omabat" <<'BINARY'
#!/bin/sh
printf '%s\n' "$*" >> "$TEST_COMMAND_LOG"
case $1 in
  version) echo v0.1.0 ;;
  help) if [ "$TEST_LEGACY_RELEASE" = true ]; then echo 'omabat install'; else echo 'omabat integrate'; fi ;;
  install) exit "${TEST_INSTALL_EXIT:-0}" ;;
  integrate) exit "${TEST_INTEGRATE_EXIT:-0}" ;;
  *) exit 99 ;;
esac
BINARY
`, 0o755)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh")
			cmd.Stdin = strings.NewReader(string(script))
			cmd.Env = append(os.Environ(),
				"PATH="+bin+":/usr/bin:/bin", "HOME="+root, "TMPDIR="+root,
				"OMABAT_VERSION=0.1.0", "OMABAT_INSTALL_DIR="+target,
				"TEST_COMMAND_LOG="+log, "TEST_INSTALL_EXIT="+tc.installExit, "TEST_INTEGRATE_EXIT="+tc.integrateExit,
				"TEST_LEGACY_RELEASE="+strconv.FormatBool(tc.legacyRelease))
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			var output strings.Builder
			cmd.Stdout, cmd.Stderr = &output, &output
			var terminalOutput <-chan string
			var master, slave *os.File
			if tc.terminal {
				master, slave = installerTerminal(t)
				defer master.Close()
				cmd.ExtraFiles = []*os.File{slave}
				// fd 0 remains the script pipe, just as in curl ... | sh.
				cmd.SysProcAttr.Setctty, cmd.SysProcAttr.Ctty = true, 3
			}
			if err := cmd.Start(); err != nil {
				if slave != nil {
					slave.Close()
				}
				t.Fatal(err)
			}
			if tc.terminal {
				slave.Close()
				done := make(chan string, 1)
				terminalOutput = done
				go func() {
					var transcript strings.Builder
					buffer := make([]byte, 1024)
					sent := 0
					for {
						n, err := master.Read(buffer)
						transcript.Write(buffer[:n])
						for sent < len(tc.replies) && strings.Count(transcript.String(), "[Y/n] ") > sent {
							io.WriteString(master, tc.replies[sent])
							sent++
						}
						if err != nil {
							done <- transcript.String()
							return
						}
					}
				}()
			}
			err := cmd.Wait()
			if ctx.Err() != nil {
				t.Fatalf("installer timed out: %s", output.String())
			}
			if (err != nil) != tc.wantFail {
				t.Fatalf("installer exit=%v, want failure=%v: %s", err, tc.wantFail, output.String())
			}
			if terminalOutput != nil {
				select {
				case transcript := <-terminalOutput:
					if !tc.wantFail && !strings.Contains(transcript, "Integrate Omabat with your desktop panel?") {
						t.Fatalf("missing English prompt: %q", transcript)
					}
					if tc.wantFail && strings.Contains(transcript, "[Y/n]") {
						t.Fatalf("prompted after daemon installation failed: %q", transcript)
					}
				case <-ctx.Done():
					t.Fatal("terminal reader did not finish")
				}
			}
			commands, err := os.ReadFile(log)
			want := "version\nhelp\n"
			if !tc.legacyRelease {
				want += "install\n"
			}
			if tc.wantIntegrate {
				want += "integrate\n"
			}
			if err != nil || string(commands) != want {
				t.Fatalf("installer commands=%q want=%q err=%v; output=%s", commands, want, err, output.String())
			}
			if !tc.wantFail && !strings.Contains(output.String(), "Installation complete.") {
				t.Fatalf("successful installation was not reported: %s", output.String())
			}
			if tc.integrateExit != "" && !strings.Contains(output.String(), "user daemon remain installed") {
				t.Fatalf("integration failure did not explain installation status: %s", output.String())
			}
		})
	}
}

func installerTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	return master, slave
}
