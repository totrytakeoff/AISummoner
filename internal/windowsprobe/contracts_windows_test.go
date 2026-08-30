//go:build windows

package windowsprobe

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestFullPipePathContract(t *testing.T) {
	path, err := FullPipePath(DefaultQtPipeName)
	if err != nil {
		t.Fatal(err)
	}
	if path != `\\.\pipe\LOCAL\AISummoner.Remote.v1` {
		t.Fatalf("unexpected native path %q", path)
	}
	for _, invalid := range []string{
		"", `AISummoner.Remote.v1`, `LOCAL\`, `LOCAL\nested\pipe`,
		`LOCAL\../escape`, "LOCAL\\line\nbreak", "LOCAL\\nul\x00byte",
	} {
		if _, err := FullPipePath(invalid); err == nil {
			t.Errorf("accepted invalid pipe name %q", invalid)
		}
	}
}

func TestPrivilegePolicySeparatesMembershipFromElevation(t *testing.T) {
	filteredAdministrator := TokenFacts{
		UserSID: "S-1-5-21-test", LogonSID: "S-1-5-5-test", SessionID: 1,
		IntegrityRID: 0x2000, Elevated: false, System: false,
	}
	if err := validateOrdinaryInteractiveUser(filteredAdministrator); err != nil {
		t.Fatalf("a non-elevated interactive administrator token must be accepted: %v", err)
	}
	for name, facts := range map[string]TokenFacts{
		"elevated":  withTokenFact(filteredAdministrator, func(value *TokenFacts) { value.Elevated = true }),
		"high":      withTokenFact(filteredAdministrator, func(value *TokenFacts) { value.IntegrityRID = mandatoryHighRID }),
		"system":    withTokenFact(filteredAdministrator, func(value *TokenFacts) { value.System = true }),
		"session 0": withTokenFact(filteredAdministrator, func(value *TokenFacts) { value.SessionID = 0 }),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateOrdinaryInteractiveUser(facts); err == nil {
				t.Fatal("unsafe privilege context was accepted")
			}
		})
	}

	facts, err := CurrentTokenFacts()
	if err != nil {
		t.Fatalf("read current runner token: %v", err)
	}
	if facts.UserSID == "" || facts.LogonSID == "" || facts.IntegrityRID == 0 {
		t.Fatalf("incomplete token facts: %+v", facts)
	}
	directory, err := LocalDataDirectory()
	if err != nil || !filepath.IsAbs(directory) || filepath.Base(directory) != remoteDataProduct {
		t.Fatalf("invalid LocalAppData contract %q: %v", directory, err)
	}
	t.Logf("runner token: session=%d integrity=%#x elevated=%v system=%v", facts.SessionID,
		facts.IntegrityRID, facts.Elevated, facts.System)
}

func TestDPAPICurrentUserIdentityRoundTripAndCorruption(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := ProtectCurrentUser(pkcs8)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, pkcs8) {
		t.Fatal("DPAPI output contains the plaintext key")
	}
	roundTrip, err := UnprotectCurrentUser(protected)
	if err != nil || !bytes.Equal(roundTrip, pkcs8) {
		t.Fatalf("DPAPI round trip failed: %v", err)
	}
	corrupted := append([]byte(nil), protected...)
	corrupted[len(corrupted)/2] ^= 0x80
	if _, err := UnprotectCurrentUser(corrupted); err == nil {
		t.Fatal("corrupted DPAPI ciphertext was accepted")
	}

	directory := filepath.Join(t.TempDir(), "RemoteClient")
	if err := WriteProtectedIdentity(directory, "device_ed25519.dpapi", pkcs8); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(directory, "device_ed25519.dpapi")
	stored, err := ReadProtectedIdentity(identityPath)
	if err != nil || !bytes.Equal(stored, pkcs8) {
		t.Fatalf("protected identity round trip failed: %v", err)
	}
	security, err := windows.GetNamedSecurityInfo(
		identityPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("inspect identity DACL: %v", err)
	}
	control, _, err := security.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("identity DACL is not protected: control=%#x err=%v", control, err)
	}
	contents, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	contents[len(contents)-1] ^= 1
	if err := os.WriteFile(identityPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProtectedIdentity(identityPath); err == nil {
		t.Fatal("corrupted identity envelope was accepted")
	}
}

func TestVerifiedPipeExclusiveConcurrentAndRestart(t *testing.T) {
	name := fmt.Sprintf(`LOCAL\AISummoner.Remote.test.%d.%d`, os.Getpid(), time.Now().UnixNano())
	path, err := FullPipePath(name)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ListenVerifiedPipe(name)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ListenVerifiedPipe(name); err == nil {
		second.Close()
		listener.Close()
		t.Fatal("a second first-instance listener acquired the live endpoint")
	}

	const clients = 4
	serverErrors := make(chan error, clients)
	go func() {
		var handlers sync.WaitGroup
		for index := 0; index < clients; index++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				serverErrors <- acceptErr
				continue
			}
			handlers.Add(1)
			go func(connection net.Conn) {
				defer handlers.Done()
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				buffer := make([]byte, 16)
				count, readErr := connection.Read(buffer)
				if readErr == nil {
					_, readErr = connection.Write(append([]byte("ack:"), buffer[:count]...))
				}
				serverErrors <- readErr
			}(connection)
		}
		handlers.Wait()
	}()

	clientErrors := make(chan error, clients)
	for index := 0; index < clients; index++ {
		go func(index int) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			connection, dialErr := winio.DialPipeAccessImpLevel(
				ctx, path, uint32(windows.GENERIC_READ|windows.GENERIC_WRITE),
				winio.PipeImpLevelImpersonation,
			)
			if dialErr != nil {
				clientErrors <- dialErr
				return
			}
			defer connection.Close()
			message := []byte(strconv.Itoa(index))
			if _, dialErr = connection.Write(message); dialErr == nil {
				response := make([]byte, 16)
				var count int
				count, dialErr = connection.Read(response)
				if dialErr == nil && string(response[:count]) != "ack:"+string(message) {
					dialErr = fmt.Errorf("unexpected response %q", response[:count])
				}
			}
			clientErrors <- dialErr
		}(index)
	}
	for index := 0; index < clients; index++ {
		if err := <-clientErrors; err != nil {
			t.Errorf("client %d: %v", index, err)
		}
		if err := <-serverErrors; err != nil {
			t.Errorf("server %d: %v", index, err)
		}
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := ListenVerifiedPipe(name)
	if err != nil {
		t.Fatalf("endpoint did not recover after close: %v", err)
	}
	restarted.Close()
}

func TestRunPowerShellOutputCwdAndExit(t *testing.T) {
	workingDirectory := t.TempDir()
	result, err := RunPowerShell(context.Background(), workingDirectory, `
[Console]::Out.WriteLine("AIS_STDOUT_中文")
[Console]::Error.WriteLine("AIS_STDERR_中文")
[Console]::Out.WriteLine((Get-Location).Path)
exit 17
`)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 17 {
		t.Fatalf("exit code=%d, want 17", result.ExitCode)
	}
	if !bytes.Contains(result.Stdout, []byte("AIS_STDOUT_中文")) ||
		!bytes.Contains(result.Stdout, []byte(workingDirectory)) {
		t.Fatalf("unexpected stdout %q", result.Stdout)
	}
	if !bytes.Contains(result.Stderr, []byte("AIS_STDERR_中文")) {
		t.Fatalf("unexpected stderr %q", result.Stderr)
	}
}

func TestRunPowerShellCancellationKillsDescendant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := RunPowerShell(ctx, t.TempDir(), `
$exe = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$child = Start-Process -FilePath $exe -ArgumentList @("-NoLogo", "-NoProfile", "-Command", "Start-Sleep -Seconds 120") -PassThru
[Console]::Out.WriteLine("AIS_CHILD_PID=" + $child.Id)
[Console]::Out.Flush()
Start-Sleep -Seconds 120
`)
	if !errors.Is(err, context.DeadlineExceeded) || !result.Cancelled {
		t.Fatalf("expected bounded cancellation, result=%+v err=%v", result, err)
	}
	childPID, err := markerPID(result.Stdout, "AIS_CHILD_PID=")
	if err != nil {
		t.Fatalf("read descendant marker from %q: %v", result.Stdout, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processStillRunning(childPID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processStillRunning(childPID) {
		t.Fatalf("descendant process %d survived Job cancellation", childPID)
	}
}

func TestConPTYUTF8ResizeInterruptAndCleanup(t *testing.T) {
	before, err := currentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		session, err := StartConPTY(t.TempDir(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Resize(101, 37); err != nil {
			session.Close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		output := &lockedBuffer{}
		marker := make(chan struct{})
		go func() {
			defer close(marker)
			for {
				select {
				case chunk, open := <-session.Output():
					if !open {
						return
					}
					output.Write(chunk)
					if output.Contains([]byte("AIS_CONPTY_DONE_中文")) {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		if err := session.Write(ctx, []byte(
			`$s=$Host.UI.RawUI.WindowSize; Write-Output ("AIS_SIZE="+$s.Width+"x"+$s.Height); Write-Output "AIS_READY"; Start-Sleep -Seconds 120`+"\r\n",
		)); err != nil {
			cancel()
			session.Close()
			t.Fatal(err)
		}
		if err := waitForBytes(ctx, output, []byte("AIS_READY")); err != nil {
			cancel()
			session.Close()
			t.Fatalf("ConPTY did not become ready: %v output=%q", err, output.Bytes())
		}
		if err := session.Write(ctx, []byte{3}); err != nil {
			cancel()
			session.Close()
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if err := session.Write(ctx, []byte(`Write-Output "AIS_CONPTY_DONE_中文"; exit 0`+"\r\n")); err != nil {
			cancel()
			session.Close()
			t.Fatal(err)
		}
		select {
		case <-marker:
		case <-ctx.Done():
		}
		if !output.Contains([]byte("AIS_CONPTY_DONE_中文")) {
			cancel()
			session.Close()
			t.Fatalf("ConPTY UTF-8/interrupt proof failed: %q", output.Bytes())
		}
		if !output.Contains([]byte("AIS_SIZE=101x37")) {
			t.Logf("ConPTY resize output was terminal-dependent: %q", output.Bytes())
		}
		if _, err := session.Wait(ctx); err != nil {
			cancel()
			session.Close()
			t.Fatal(err)
		}
		cancel()
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := currentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > before+8 {
		t.Fatalf("native handle growth after repeated ConPTY sessions: before=%d after=%d", before, after)
	}
}

func withTokenFact(value TokenFacts, change func(*TokenFacts)) TokenFacts {
	change(&value)
	return value
}

func markerPID(output []byte, prefix string) (uint32, error) {
	text := string(output)
	index := strings.Index(text, prefix)
	if index < 0 {
		return 0, errors.New("PID marker not found")
	}
	text = text[index+len(prefix):]
	end := strings.IndexAny(text, "\r\n")
	if end >= 0 {
		text = text[:end]
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
	return uint32(parsed), err
}

func processStillRunning(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && result == uint32(windows.WAIT_TIMEOUT)
}

func waitForBytes(ctx context.Context, output *lockedBuffer, marker []byte) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if output.Contains(marker) {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type lockedBuffer struct {
	mutex    sync.Mutex
	contents bytes.Buffer
}

func (buffer *lockedBuffer) Write(contents []byte) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	_, _ = buffer.contents.Write(contents)
}

func (buffer *lockedBuffer) Contains(marker []byte) bool {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return bytes.Contains(buffer.contents.Bytes(), marker)
}

func (buffer *lockedBuffer) Bytes() []byte {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return append([]byte(nil), buffer.contents.Bytes()...)
}

var getProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func currentProcessHandleCount() (uint32, error) {
	var count uint32
	result, _, callErr := getProcessHandleCount.Call(
		uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)),
	)
	if result == 0 {
		return 0, callErr
	}
	return count, nil
}
