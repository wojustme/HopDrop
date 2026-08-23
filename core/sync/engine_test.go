package sync

import (
	"context"
	"os"
	"path/filepath"
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
	recv := NewNode("Recv", protocol.PlatformLinux, device.NewID(), recvSink)
	recv.OnDecision(func(protocol.DeviceInfo, protocol.Offer) protocol.Decision {
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
	if err := recv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer recv.Stop()

	// 发送端：只发不收。
	send := NewNode("Send", protocol.PlatformMacOS, device.NewID(), nil)
	if err := send.Start(0); err != nil {
		t.Fatal(err)
	}
	defer send.Stop()

	// 直连接收端端点发送（不依赖组播发现，测试更稳定）。
	endpoint := "127.0.0.1:" + itoa(recv.Self().SyncPort)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paths := []string{filepath.Join(srcDir, "hello.txt"), filepath.Join(srcDir, "album")}
	if err := send.SendToEndpoint(ctx, endpoint, paths); err != nil {
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
	recv := NewNode("Recv", protocol.PlatformLinux, device.NewID(), recvSink)
	recv.OnDecision(func(protocol.DeviceInfo, protocol.Offer) protocol.Decision {
		return protocol.Decision{Accept: false, Reason: "test reject"}
	})
	if err := recv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer recv.Stop()

	send := NewNode("Send", protocol.PlatformMacOS, device.NewID(), nil)
	if err := send.Start(0); err != nil {
		t.Fatal(err)
	}
	defer send.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "127.0.0.1:" + itoa(recv.Self().SyncPort)
	if err := send.SendToEndpoint(ctx, endpoint, []string{filepath.Join(srcDir, "secret.txt")}); err != nil {
		t.Fatalf("send returned error on reject: %v", err)
	}

	entries, _ := os.ReadDir(dlDir)
	if len(entries) != 0 {
		t.Errorf("expected empty download dir on reject, got %d entries", len(entries))
	}
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
