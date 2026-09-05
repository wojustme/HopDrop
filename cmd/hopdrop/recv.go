package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
	syncpkg "github.com/xurenhe/hopdrop/core/sync"
)

// cmdRecv 启动一个常驻接收端：发现自身可被其他设备看到，并把推送来的文件落地。
func cmdRecv(args []string) {
	fs := flag.NewFlagSet("recv", flag.ExitOnError)
	dir := fs.String("dir", "", "下载目录（必填），收到的文件会落到这里")
	name := fs.String("name", defaultName(), "设备名")
	port := fs.Int("port", 0, "TCP 端口，0 表示自动分配")
	yes := fs.Bool("yes", false, "自动接受所有传入的文件（不再逐次询问）")
	_ = fs.Parse(args)

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "错误: 必须用 --dir 指定下载目录")
		fs.Usage()
		os.Exit(2)
	}
	abs, _ := filepath.Abs(*dir)

	sink, err := filestore.NewDirSink(abs)
	if err != nil {
		fatal(err)
	}
	deviceID, err := device.LoadOrCreateID(filepath.Join(abs, filestore.MetaDir, "device_id"))
	if err != nil {
		fatal(err)
	}

	node := syncpkg.NewNode(*name, currentPlatform(), deviceID, sink)
	// 桌面/CLI 用标准 mDNS 发现，能与 iOS Bonjour / Android NsdManager 互通。
	node.SetDiscovery(syncpkg.DiscoveryMDNS)

	// 决策回调：--yes 直接全收；否则在终端里询问。
	var askMu sync.Mutex
	node.OnDecision(func(peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision {
		if *yes {
			fmt.Printf("\n[recv] 接受来自 %s 的 %d 个文件（%s）\n", peer.Name, len(offer.Files), humanBytes(offer.TotalBytes))
			return protocol.Decision{Accept: true}
		}
		askMu.Lock()
		defer askMu.Unlock()
		fmt.Printf("\n[recv] %s 想发送 %d 个文件（共 %s）:\n", peer.Name, len(offer.Files), humanBytes(offer.TotalBytes))
		for _, f := range offer.Files {
			fmt.Printf("    - %s (%s)\n", f.RelPath, humanBytes(f.Size))
		}
		fmt.Print("接收? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(line), "y") {
			return protocol.Decision{Accept: true}
		}
		return protocol.Decision{Accept: false, Reason: "user rejected"}
	})

	node.OnProgress(func(p syncpkg.Progress) {
		if p.Direction != "recv" {
			return
		}
		switch p.Phase {
		case "transfer":
			fmt.Printf("\r[recv] 收 %d/%d  %s  (%s/%s)      ",
				p.Files, p.TotalFiles, p.CurrentName, humanBytes(p.Bytes), humanBytes(p.TotalBytes))
		case "done":
			fmt.Printf("\r[recv] 完成: 收到 %d 个文件, 共 %s          \n", p.Files, humanBytes(p.Bytes))
		case "rejected":
			fmt.Printf("\r[recv] 已拒绝该传输                         \n")
		case "error":
			fmt.Printf("\n[recv] 出错: %s\n", p.Err)
		}
	})

	if err := node.Start(*port); err != nil {
		fatal(err)
	}
	self := node.Self()
	fmt.Printf("HopDrop 接收端已启动\n  设备: %s (%s)\n  下载目录: %s\n  监听: :%d\n  自动接受: %v\n\n等待其他设备发送… 按 Ctrl-C 退出。\n\n",
		self.Name, self.ID[:8], abs, self.SyncPort, *yes)

	waitForSignal()
	fmt.Println("\n正在退出…")
	node.Stop()
}

func cmdPeers(args []string) {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	name := fs.String("name", defaultName()+"-probe", "设备名")
	timeout := fs.Int("timeout", 10, "监听时长（秒）")
	_ = fs.Parse(args)

	self := protocol.DeviceInfo{
		ID:       device.NewID(),
		Name:     *name,
		Platform: currentPlatform(),
	}
	disc := discovery.New(self)
	if err := disc.Start(); err != nil {
		fatal(err)
	}
	defer disc.Stop()

	fmt.Printf("正在监听局域网内的 HopDrop 设备（%d 秒）…\n\n", *timeout)
	ctx, cancel := context.WithTimeout(context.Background(), durationSeconds(*timeout))
	defer cancel()
	events := disc.Events()
	for {
		select {
		case <-ctx.Done():
			printPeers(disc.Peers())
			return
		case ev := <-events:
			if ev.Online {
				fmt.Printf("发现: %s (%s @ %s)\n", ev.Peer.Device.Name, ev.Peer.Device.Platform, ev.Peer.SyncEndpoint())
			}
		}
	}
}

func printPeers(peers []discovery.Peer) {
	fmt.Printf("\n当前在线设备 %d 台:\n", len(peers))
	for _, p := range peers {
		fmt.Printf("  - %-16s %-8s %s\n", p.Device.Name, p.Device.Platform, p.SyncEndpoint())
	}
}

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
}
