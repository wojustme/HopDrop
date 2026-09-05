package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/protocol"
	syncpkg "github.com/xurenhe/hopdrop/core/sync"
)

// cmdSend 把一批本地文件/目录发送给某台设备。
//
// 目标可通过 --to 指定：
//   - "host:port"（含冒号）：直连该端点，跳过发现。
//   - 其他字符串：作为设备名/前缀，在发现到的在线设备里匹配。
//
// 不带 --to 时，会先发现一段时间并列出在线设备让用户按序号选择。
func cmdSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	to := fs.String("to", "", "目标设备名/前缀，或 host:port 直连端点")
	name := fs.String("name", defaultName()+"-send", "本端设备名")
	timeout := fs.Int("timeout", 6, "发现在线设备的等待时长（秒）")
	_ = fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "错误: 请至少指定一个要发送的文件或目录")
		fs.Usage()
		os.Exit(2)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			fatal(fmt.Errorf("无法访问 %s: %w", p, err))
		}
	}

	self := protocol.DeviceInfo{
		ID:       device.NewID(),
		Name:     *name,
		Platform: currentPlatform(),
	}
	// 发送端也需要一个 sink 占位（不会被用到，因为我们只主动发送）。
	node := syncpkg.NewNode(self.Name, self.Platform, self.ID, nil)
	// 与接收端一致，用标准 mDNS 发现。
	node.SetDiscovery(syncpkg.DiscoveryMDNS)
	node.OnProgress(func(p syncpkg.Progress) {
		if p.Direction != "send" {
			return
		}
		switch p.Phase {
		case "offer":
			fmt.Printf("[send] 已向 %s 发出清单：%d 个文件（%s），等待对方确认…\n",
				p.PeerName, p.TotalFiles, humanBytes(p.TotalBytes))
		case "transfer":
			fmt.Printf("\r[send] 发 %d/%d  %s  (%s/%s)      ",
				p.Files, p.TotalFiles, p.CurrentName, humanBytes(p.Bytes), humanBytes(p.TotalBytes))
		case "done":
			fmt.Printf("\r[send] 完成: 发送 %d 个文件, 共 %s          \n", p.Files, humanBytes(p.Bytes))
		case "rejected":
			msg := "对方拒绝了本次传输"
			if p.Err != "" {
				msg += "（" + p.Err + "）"
			}
			fmt.Printf("\r[send] %s                         \n", msg)
		case "error":
			fmt.Printf("\n[send] 出错: %s\n", p.Err)
		}
	})

	if err := node.Start(0); err != nil {
		fatal(err)
	}
	defer node.Stop()

	ctx := context.Background()

	// 直连端点：形如 host:port。
	if *to != "" && strings.Contains(*to, ":") && !looksLikeName(*to) {
		if err := node.SendToEndpoint(ctx, *to, paths); err != nil {
			fatal(err)
		}
		return
	}

	// 否则走发现，找到目标 peer。
	peer, err := discoverTarget(node, *to, *timeout)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("[send] 目标: %s (%s @ %s)\n", peer.Device.Name, peer.Device.Platform, peer.SyncEndpoint())
	if err := node.SendPaths(ctx, peer, paths); err != nil {
		fatal(err)
	}
}

// discoverTarget 在超时时间内发现在线设备：
//   - 若给定 want（设备名/前缀），匹配到第一台即返回。
//   - 否则收集完毕后打印列表让用户按序号选择。
func discoverTarget(node *syncpkg.Node, want string, timeoutSec int) (discovery.Peer, error) {
	deadline := time.Now().Add(durationSeconds(timeoutSec))
	seen := map[string]discovery.Peer{}
	fmt.Printf("正在发现在线设备（最多 %d 秒）…\n", timeoutSec)
	for time.Now().Before(deadline) {
		for _, p := range node.Peers() {
			seen[p.Device.ID] = p
			if want != "" && matchName(p.Device.Name, want) {
				return p, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	peers := make([]discovery.Peer, 0, len(seen))
	for _, p := range seen {
		peers = append(peers, p)
	}
	if len(peers) == 0 {
		return discovery.Peer{}, fmt.Errorf("未发现任何在线设备；确认目标设备已执行 `hopdrop recv`")
	}
	if want != "" {
		return discovery.Peer{}, fmt.Errorf("未找到名字匹配 %q 的在线设备", want)
	}
	return pickPeer(peers)
}

// pickPeer 打印在线设备并让用户在终端里按序号选择。
func pickPeer(peers []discovery.Peer) (discovery.Peer, error) {
	fmt.Println("发现以下设备：")
	for i, p := range peers {
		fmt.Printf("  [%d] %-16s %-8s %s\n", i+1, p.Device.Name, p.Device.Platform, p.SyncEndpoint())
	}
	fmt.Print("选择目标序号: ")
	var idx int
	if _, err := fmt.Scanf("%d", &idx); err != nil || idx < 1 || idx > len(peers) {
		return discovery.Peer{}, fmt.Errorf("无效的选择")
	}
	return peers[idx-1], nil
}

func matchName(name, want string) bool {
	return strings.EqualFold(name, want) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(want))
}

// looksLikeName 粗略判断 --to 是否更像设备名而非 host:port。
// 端点里的冒号后应是纯数字端口；否则当作名字（如 IPv6 需用户显式带端口，这里从简）。
func looksLikeName(s string) bool {
	i := strings.LastIndex(s, ":")
	if i < 0 || i == len(s)-1 {
		return true
	}
	for _, c := range s[i+1:] {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false
}
