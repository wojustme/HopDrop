// Command hopdrop-gui 是 HopDrop 的跨平台桌面客户端（基于 Fyne）。
//
// 一份 Go 代码即可编译出 macOS / Windows / Linux 三端原生 GUI，与核心库同语言、
// 零跨语言桥接。它复用 core/sync.Node 提供的能力：
//   - 左侧实时展示局域网内发现到的在线设备；
//   - 选中设备后点“发送文件/发送文件夹”挑选内容推送；
//   - 收到他人发来的传输时弹窗询问接受/拒绝，接受后落到下载目录；
//   - 底部展示当前传输进度。
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
	syncpkg "github.com/xurenhe/hopdrop/core/sync"
)

// appIcon 内嵌应用图标，用于设置窗口图标（打包时 fyne package 另会用 Icon.png 作应用图标）。
//
//go:embed Icon.png
var appIcon []byte

// ui 汇集应用运行期的所有可变状态与控件引用。
type ui struct {
	win  fyne.Window
	node *syncpkg.Node

	mu        sync.Mutex
	peers     []discovery.Peer // 当前在线设备（按名字排序）
	selected  int              // 列表中选中的行，-1 表示未选
	peerList  *widget.List
	statusLbl *widget.Label
	progress  *widget.ProgressBar
	dlDirLbl  *widget.Label

	downloadDir string
	// pendingOffer 用于把后台收到的 Offer 决策转交主线程弹窗处理。
	decisionCh chan bool
}

func main() {
	a := app.NewWithID("com.hopdrop.desktop")
	a.SetIcon(fyne.NewStaticResource("icon.png", appIcon))
	w := a.NewWindow("HopDrop")
	w.Resize(fyne.NewSize(560, 460))

	u := &ui{
		win:         w,
		selected:    -1,
		downloadDir: defaultDownloadDir(),
	}

	sink, err := filestore.NewDirSink(u.downloadDir)
	if err != nil {
		dialog.ShowError(err, w)
	}
	deviceID, _ := device.LoadOrCreateID(filepath.Join(u.downloadDir, filestore.MetaDir, "device_id"))
	name := deviceName()

	u.node = syncpkg.NewNode(name, currentPlatform(), deviceID, sink)
	u.node.OnPeer(func(ev discovery.PeerEvent) { u.refreshPeers() })
	u.node.OnDecision(u.onDecision)
	u.node.OnProgress(u.onProgress)

	w.SetContent(u.buildUI(name))

	if err := u.node.Start(0); err != nil {
		dialog.ShowError(fmt.Errorf("启动失败: %w", err), w)
	}
	w.SetOnClosed(func() { u.node.Stop() })

	w.ShowAndRun()
}

func (u *ui) buildUI(name string) fyne.CanvasObject {
	self := u.node.Self()
	title := widget.NewLabel(fmt.Sprintf("本机: %s (%s)", name, self.Platform))
	title.TextStyle = fyne.TextStyle{Bold: true}

	u.peerList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.peers) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			defer u.mu.Unlock()
			if i < len(u.peers) {
				p := u.peers[i]
				o.(*widget.Label).SetText(fmt.Sprintf("%s  ·  %s  @ %s", p.Device.Name, p.Device.Platform, p.SyncEndpoint()))
			}
		},
	)
	u.peerList.OnSelected = func(id widget.ListItemID) { u.mu.Lock(); u.selected = id; u.mu.Unlock() }

	sendFileBtn := widget.NewButton("发送文件…", func() { u.pickAndSend(false) })
	sendDirBtn := widget.NewButton("发送文件夹…", func() { u.pickAndSend(true) })
	refreshBtn := widget.NewButton("刷新设备", func() { u.refreshPeers() })

	u.dlDirLbl = widget.NewLabel("下载目录: " + u.downloadDir)
	chooseDlBtn := widget.NewButton("更改下载目录…", u.chooseDownloadDir)

	u.statusLbl = widget.NewLabel("就绪。等待设备…")
	u.progress = widget.NewProgressBar()
	u.progress.Hide()

	buttons := container.NewHBox(sendFileBtn, sendDirBtn, refreshBtn)
	bottom := container.NewVBox(
		container.NewBorder(nil, nil, nil, chooseDlBtn, u.dlDirLbl),
		u.statusLbl,
		u.progress,
	)
	body := container.NewBorder(
		container.NewVBox(title, widget.NewLabel("在线设备（点选后发送）:"), buttons),
		bottom, nil, nil,
		u.peerList,
	)
	return body
}

// refreshPeers 从 node 拉取最新在线设备并刷新列表（在 UI 线程安全地更新）。
func (u *ui) refreshPeers() {
	peers := u.node.Peers()
	sort.Slice(peers, func(i, j int) bool { return peers[i].Device.Name < peers[j].Device.Name })
	u.mu.Lock()
	u.peers = peers
	u.mu.Unlock()
	fyne.Do(func() { u.peerList.Refresh() })
}

// pickAndSend 弹出文件/文件夹选择器，选中后向当前选中的设备发送。
func (u *ui) pickAndSend(folder bool) {
	u.mu.Lock()
	sel := u.selected
	var target discovery.Peer
	if sel >= 0 && sel < len(u.peers) {
		target = u.peers[sel]
	}
	u.mu.Unlock()
	if sel < 0 {
		dialog.ShowInformation("请先选择设备", "请在上方列表里点选一个目标设备。", u.win)
		return
	}

	if folder {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err != nil || list == nil {
				return
			}
			u.startSend(target, []string{list.Path()})
		}, u.win)
		return
	}
	dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			return
		}
		path := rc.URI().Path()
		rc.Close()
		u.startSend(target, []string{path})
	}, u.win)
}

func (u *ui) startSend(target discovery.Peer, paths []string) {
	fyne.Do(func() {
		u.statusLbl.SetText(fmt.Sprintf("正在发送到 %s …", target.Device.Name))
		u.progress.SetValue(0)
		u.progress.Show()
	})
	go func() {
		err := u.node.SendPaths(context.Background(), target, paths)
		fyne.Do(func() {
			if err != nil {
				u.progress.Hide()
				dialog.ShowError(err, u.win)
				u.statusLbl.SetText("发送失败。")
			}
		})
	}()
}

// onDecision 在后台收到 Offer 时被调用；这里同步弹窗等待用户点击接受/拒绝。
func (u *ui) onDecision(peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision {
	ch := make(chan bool, 1)
	msg := fmt.Sprintf("%s 想给你发送 %d 个文件（共 %s）。\n是否接收到:\n%s ？",
		peer.Name, len(offer.Files), humanBytes(offer.TotalBytes), u.downloadDir)
	fyne.Do(func() {
		dialog.ShowConfirm("收到文件传输", msg, func(ok bool) { ch <- ok }, u.win)
	})
	accept := <-ch
	return protocol.Decision{Accept: accept}
}

func (u *ui) onProgress(p syncpkg.Progress) {
	fyne.Do(func() {
		switch p.Phase {
		case "transfer":
			var frac float64
			if p.TotalBytes > 0 {
				frac = float64(p.Bytes) / float64(p.TotalBytes)
			}
			u.progress.Show()
			u.progress.SetValue(frac)
			verb := "接收"
			if p.Direction == "send" {
				verb = "发送"
			}
			u.statusLbl.SetText(fmt.Sprintf("%s %d/%d  %s  (%s/%s)",
				verb, p.Files, p.TotalFiles, p.CurrentName, humanBytes(p.Bytes), humanBytes(p.TotalBytes)))
		case "done":
			u.progress.SetValue(1)
			u.progress.Hide()
			verb := "接收"
			if p.Direction == "send" {
				verb = "发送"
			}
			u.statusLbl.SetText(fmt.Sprintf("%s完成: %d 个文件, 共 %s", verb, p.Files, humanBytes(p.Bytes)))
		case "rejected":
			u.progress.Hide()
			u.statusLbl.SetText("对方拒绝了本次传输。")
		case "error":
			u.progress.Hide()
			u.statusLbl.SetText("出错: " + p.Err)
		}
	})
}

func (u *ui) chooseDownloadDir() {
	dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
		if err != nil || list == nil {
			return
		}
		newDir := list.Path()
		sink, serr := filestore.NewDirSink(newDir)
		if serr != nil {
			dialog.ShowError(serr, u.win)
			return
		}
		u.mu.Lock()
		u.downloadDir = newDir
		u.mu.Unlock()
		// 重建 node 让新的 sink 生效。
		u.node.Stop()
		self := u.node.Self()
		u.node = syncpkg.NewNode(self.Name, self.Platform, self.ID, sink)
		u.node.OnPeer(func(ev discovery.PeerEvent) { u.refreshPeers() })
		u.node.OnDecision(u.onDecision)
		u.node.OnProgress(u.onProgress)
		_ = u.node.Start(0)
		u.dlDirLbl.SetText("下载目录: " + newDir)
	}, u.win)
}

var _ = storage.NewFileURI // 保留 storage 依赖引用，便于后续扩展书签

func defaultDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, "Downloads", "HopDrop")
		return d
	}
	return filepath.Join(os.TempDir(), "HopDrop")
}

func deviceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "HopDrop-Desktop"
}
