package sync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// TestSendReceiveEndToEnd 用两台本地 Node 走完整链路验证：
// 发送端推送一个文件和一个含子目录的文件夹，接收端接受并落地，
// 校验文件数量、相对路径与内容都正确。
func TestSendReceiveEndToEnd(t *testing.T) {
	srcDir := t.TempDir()
	dlDir := t.TempDir()

	// 准备发送内容：一个顶层文件 + 一个含子目录的文件夹。
	writeFile(t, filepath.Join(srcDir, "hello.txt"), "hello hopdrop")
	writeFile(t, filepath.Join(srcDir, "album", "a.txt"), "AAA")
	writeFile(t, filepath.Join(srcDir, "album", "sub", "b.txt"), "BBBBB")

	// 接收端：自动接受全部。
	recvSink, err := filestore.NewDirSink(dlDir)
	if err != nil {
		t.Fatal(err)
	}
	recvDone := make(chan struct{})
	recv := NewNode("Recv", protocol.PlatformLinux, testIdentity(t), recvSink)
	recv.UseManualDiscovery()
	recv.OnDecision(func(context.Context, protocol.DeviceInfo, protocol.Offer) protocol.Decision {
		return protocol.Decision{Accept: true}
	})
	recv.OnProgress(func(p Progress) {
		if p.Direction == "recv" && p.Phase == "done" {
			select {
			case <-recvDone:
			default:
				close(recvDone)
			}
		}
	})
	if err := recv.StartServer(0); err != nil {
		t.Fatal(err)
	}
	defer recv.Stop()

	// 发送端：只发不收。
	send := NewNode("Send", protocol.PlatformMacOS, testIdentity(t), nil)
	send.UseManualDiscovery()
	if err := send.StartClient(); err != nil {
		t.Fatal(err)
	}
	defer send.Stop()

	// 直连接收端端点发送（不依赖 Bonjour 发现，测试更稳定）。
	endpoint := "127.0.0.1:" + itoa(recv.Self().SyncPort)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paths := []string{filepath.Join(srcDir, "hello.txt"), filepath.Join(srcDir, "album")}
	if err := send.SendToEndpoint(ctx, endpoint, recv.Self().Fingerprint, paths); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// 发送返回后，接收端可能仍在落地最后一个文件；等待其 done 事件（或超时）。
	select {
	case <-recvDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for receiver to finish")
	}

	// 校验落地结果。
	want := map[string]string{
		"hello.txt":       "hello hopdrop",
		"album/a.txt":     "AAA",
		"album/sub/b.txt": "BBBBB",
	}
	for rel, content := range want {
		got, err := os.ReadFile(filepath.Join(dlDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("missing %s: %v", rel, err)
			continue
		}
		if string(got) != content {
			t.Errorf("%s content = %q, want %q", rel, got, content)
		}
	}
}

// TestReceiverRejects 验证接收方拒绝时不落地任何文件。
func TestReceiverRejects(t *testing.T) {
	srcDir := t.TempDir()
	dlDir := t.TempDir()
	writeFile(t, filepath.Join(srcDir, "secret.txt"), "nope")

	recvSink, _ := filestore.NewDirSink(dlDir)
	recv := NewNode("Recv", protocol.PlatformLinux, testIdentity(t), recvSink)
	recv.UseManualDiscovery()
	recv.OnDecision(func(context.Context, protocol.DeviceInfo, protocol.Offer) protocol.Decision {
		return protocol.Decision{Accept: false, Reason: "test reject"}
	})
	if err := recv.StartServer(0); err != nil {
		t.Fatal(err)
	}
	defer recv.Stop()

	send := NewNode("Send", protocol.PlatformMacOS, testIdentity(t), nil)
	send.UseManualDiscovery()
	if err := send.StartClient(); err != nil {
		t.Fatal(err)
	}
	defer send.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "127.0.0.1:" + itoa(recv.Self().SyncPort)
	if err := send.SendToEndpoint(ctx, endpoint, recv.Self().Fingerprint, []string{filepath.Join(srcDir, "secret.txt")}); err != nil {
		t.Fatalf("send returned error on reject: %v", err)
	}

	entries, _ := os.ReadDir(dlDir)
	if len(entries) != 0 {
		t.Errorf("expected empty download dir on reject, got %d entries", len(entries))
	}
}

func TestPartialAcceptance(t *testing.T) {
	sourceDir := t.TempDir()
	downloadDir := t.TempDir()
	writeFile(t, filepath.Join(sourceDir, "a.txt"), "A")
	writeFile(t, filepath.Join(sourceDir, "b.txt"), "BB")

	sink, _ := filestore.NewDirSink(downloadDir)
	receiver := NewNode("Receiver", protocol.PlatformLinux, testIdentity(t), sink)
	receiver.UseManualDiscovery()
	receiver.OnDecision(func(_ context.Context, _ protocol.DeviceInfo, offer protocol.Offer) protocol.Decision {
		return protocol.Decision{Accept: true, AcceptIDs: []string{offer.Files[1].ID}}
	})
	if err := receiver.StartServer(0); err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()

	sender := NewNode("Sender", protocol.PlatformLinux, testIdentity(t), nil)
	sender.UseManualDiscovery()
	if err := sender.StartClient(); err != nil {
		t.Fatal(err)
	}
	defer sender.Stop()

	endpoint := "127.0.0.1:" + itoa(receiver.Self().SyncPort)
	err := sender.SendToEndpoint(testContext(t), endpoint, receiver.Self().Fingerprint,
		[]string{filepath.Join(sourceDir, "a.txt"), filepath.Join(sourceDir, "b.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(downloadDir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("unaccepted file was written")
	}
	data, err := os.ReadFile(filepath.Join(downloadDir, "b.txt"))
	if err != nil || string(data) != "BB" {
		t.Fatalf("accepted file = %q, %v", data, err)
	}
}

func TestDirectTransferPinsReceiver(t *testing.T) {
	downloadDir := t.TempDir()
	sourceDir := t.TempDir()
	writeFile(t, filepath.Join(sourceDir, "file.txt"), "content")
	sink, _ := filestore.NewDirSink(downloadDir)
	receiver := NewNode("Receiver", protocol.PlatformLinux, testIdentity(t), sink)
	receiver.UseManualDiscovery()
	if err := receiver.StartServer(0); err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()

	sender := NewNode("Sender", protocol.PlatformLinux, testIdentity(t), nil)
	sender.UseManualDiscovery()
	if err := sender.StartClient(); err != nil {
		t.Fatal(err)
	}
	defer sender.Stop()

	endpoint := "127.0.0.1:" + itoa(receiver.Self().SyncPort)
	wrongFingerprint := strings.Repeat("0", 64)
	if err := sender.SendToEndpoint(testContext(t), endpoint, wrongFingerprint,
		[]string{filepath.Join(sourceDir, "file.txt")}); err == nil {
		t.Fatal("transfer succeeded with the wrong receiver fingerprint")
	}
}

func TestNodeCanRestartAfterStartFailure(t *testing.T) {
	node := NewNode("client", protocol.PlatformMacOS, testIdentity(t), nil)
	node.UseManualDiscovery()
	if err := node.StartServer(-1); err == nil {
		t.Fatal("invalid port unexpectedly started")
	}
	if err := node.StartClient(); err != nil {
		t.Fatalf("node did not recover from failed start: %v", err)
	}
	node.Stop()
}

func TestShortSourceIsNotFinalized(t *testing.T) {
	downloadDir := t.TempDir()
	sink, _ := filestore.NewDirSink(downloadDir)
	receiver := NewNode("Receiver", protocol.PlatformLinux, testIdentity(t), sink)
	receiver.UseManualDiscovery()
	if err := receiver.StartServer(0); err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()

	sender := NewNode("Sender", protocol.PlatformLinux, testIdentity(t), nil)
	sender.UseManualDiscovery()
	if err := sender.StartClient(); err != nil {
		t.Fatal(err)
	}
	defer sender.Stop()

	source := &shortSource{}
	endpoint := "127.0.0.1:" + itoa(receiver.Self().SyncPort)
	if err := sender.SendToEndpointSource(testContext(t), endpoint, receiver.Self().Fingerprint, source); err == nil {
		t.Fatal("short source unexpectedly succeeded")
	}
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed transfer left %d files", len(entries))
	}
}

func TestMacIOSAndroidProtocolMatrix(t *testing.T) {
	platforms := []protocol.Platform{
		protocol.PlatformMacOS,
		protocol.PlatformIOS,
		protocol.PlatformAndroid,
	}
	for _, senderPlatform := range platforms {
		for _, receiverPlatform := range platforms {
			if senderPlatform == receiverPlatform {
				continue
			}
			name := string(senderPlatform) + "_to_" + string(receiverPlatform)
			t.Run(name, func(t *testing.T) {
				sourceDir := t.TempDir()
				downloadDir := t.TempDir()
				writeFile(t, filepath.Join(sourceDir, "matrix.txt"), name)
				sink, err := filestore.NewDirSink(downloadDir)
				if err != nil {
					t.Fatal(err)
				}
				receiver := NewNode("receiver", receiverPlatform, testIdentity(t), sink)
				receiver.UseManualDiscovery()
				if err := receiver.StartServer(0); err != nil {
					t.Fatal(err)
				}
				defer receiver.Stop()

				sender := NewNode("sender", senderPlatform, testIdentity(t), nil)
				sender.UseManualDiscovery()
				if err := sender.StartClient(); err != nil {
					t.Fatal(err)
				}
				defer sender.Stop()

				endpoint := "127.0.0.1:" + itoa(receiver.Self().SyncPort)
				if err := sender.SendToEndpoint(testContext(t), endpoint, receiver.Self().Fingerprint,
					[]string{filepath.Join(sourceDir, "matrix.txt")}); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(downloadDir, "matrix.txt"))
				if err != nil || string(got) != name {
					t.Fatalf("received %q, %v", got, err)
				}
			})
		}
	}
}

type shortSource struct{}

func (*shortSource) List() ([]protocol.FileMeta, error) {
	return []protocol.FileMeta{{ID: "1", Name: "short.txt", RelPath: "short.txt", Size: 10}}, nil
}

func (*shortSource) Open(string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("short")), nil
}

func testIdentity(t *testing.T) *device.Identity {
	t.Helper()
	identity, err := device.NewIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
