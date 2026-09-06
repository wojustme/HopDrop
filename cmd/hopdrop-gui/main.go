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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
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
	peerCount *widget.Label     // 头部「在线设备（N）」计数
	emptyHint fyne.CanvasObject // 无设备时的占位提示（有设备时隐藏）
	statusLbl *widget.Label
	statusDot *canvas.Circle // 状态指示灯，随传输状态变色
	progress  *widget.ProgressBar
	dlDirLbl  *widget.Label

	downloadDir string
	// pendingOffer 用于把后台收到的 Offer 决策转交主线程弹窗处理。
	decisionCh chan bool
}

func main() {
	a := app.NewWithID("com.hopdrop.desktop")
	// 应用自定义主题：米白底 + 深青强调色，强制亮色以保证主视觉一致。
	a.Settings().SetTheme(hopTheme{forceLight: true})
	a.SetIcon(fyne.NewStaticResource("icon.png", appIcon))
	w := a.NewWindow("HopDrop")
	w.Resize(fyne.NewSize(600, 520))

	u := &ui{
		win:         w,
		selected:    -1,
		downloadDir: defaultDownloadDir(),
	}

	sink, err := filestore.NewDirSink(u.downloadDir)
	if err != nil {
		dialog.ShowError(err, w)
	}
	identity, err := device.LoadOrCreateIdentity(desktopIdentityPath())
	if err != nil {
		dialog.ShowError(err, w)
		return
	}
	name := deviceName()

	u.node = syncpkg.NewNode(name, currentPlatform(), identity, sink)
	u.node.OnPeer(func(ev discovery.PeerEvent) { u.refreshPeers() })
	u.node.OnDecision(u.onDecision)
	u.node.OnProgress(u.onProgress)

	w.SetContent(u.buildUI(name))

	// 固定监听端口，让手机「扫一次码」后即便桌面重启（端口不变）仍可直连；
	// 端口被占用时回退到系统随机分配，避免启动失败。
	if err := u.startNode(); err != nil {
		dialog.ShowError(fmt.Errorf("启动失败: %w", err), w)
	}
	w.SetOnClosed(func() { u.node.Stop() })

	w.ShowAndRun()
}

// defaultSyncPort 是桌面端优先监听的固定端口，配合手动配对二维码实现「扫一次长期可用」。
const defaultSyncPort = 47772

// startNode 先尝试固定端口，占用时回退随机端口。
func (u *ui) startNode() error {
	if err := u.node.StartServer(defaultSyncPort); err != nil {
		return u.node.StartServer(0)
	}
	return nil
}

func (u *ui) buildUI(name string) fyne.CanvasObject {
	self := u.node.Self()

	// —— 顶部品牌头（HUD 风格）：左侧应用标识，右侧本机身份胶囊 ——
	header := u.buildHeader(name, self)

	// —— 在线设备列表 ——
	u.peerCount = widget.NewLabel("在线设备 (0)")
	u.peerCount.TextStyle = fyne.TextStyle{Bold: true}

	u.peerList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.peers) },
		func() fyne.CanvasObject { return newPeerRow() },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			defer u.mu.Unlock()
			if i < len(u.peers) {
				o.(*peerRow).set(u.peers[i])
			}
		},
	)
	u.peerList.OnSelected = func(id widget.ListItemID) { u.mu.Lock(); u.selected = id; u.mu.Unlock() }

	// 空状态提示叠加在列表上，未发现设备时给出扫描中的引导文案；有设备后隐藏，避免与设备行重叠。
	u.emptyHint = container.NewCenter(container.NewVBox(
		newCenteredIcon(theme.SearchIcon(), 44),
		newMutedLabel("正在扫描局域网设备…", true),
		newMutedLabel("确保设备处于同一 Wi-Fi 网络", false),
	))
	listStack := container.NewStack(u.emptyHint, u.peerList)

	deviceHeader := container.NewBorder(nil, nil, u.peerCount, nil)
	deviceCard := newPanel("", container.NewBorder(
		container.NewVBox(deviceHeader, widget.NewSeparator()), nil, nil, nil,
		listStack,
	))

	// —— 操作区：发送 / 手动配对 / 刷新 ——
	sendFileBtn := widget.NewButtonWithIcon("发送文件", theme.MailSendIcon(), func() { u.pickAndSend(false) })
	sendFileBtn.Importance = widget.HighImportance
	sendDirBtn := widget.NewButtonWithIcon("发送文件夹", theme.FolderOpenIcon(), func() { u.pickAndSend(true) })
	sendDirBtn.Importance = widget.HighImportance
	pairBtn := widget.NewButtonWithIcon("手动配对", theme.ContentAddIcon(), func() { u.showPairing() })
	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() { u.refreshPeers() })

	buttons := container.NewGridWithColumns(4, sendFileBtn, sendDirBtn, pairBtn, refreshBtn)

	// —— 状态条：指示灯 + 文案 + 进度 ——
	u.statusDot = canvas.NewCircle(theme.Color(theme.ColorNamePlaceHolder))
	u.statusDot.Resize(fyne.NewSize(10, 10))
	dotWrap := container.NewGridWrap(fyne.NewSize(12, 12), u.statusDot)

	u.statusLbl = widget.NewLabel("就绪 · 等待设备接入")
	u.progress = widget.NewProgressBar()
	u.progress.Hide()

	statusRow := container.NewBorder(nil, nil, container.NewCenter(dotWrap), nil, u.statusLbl)
	statusCard := newPanel("", container.NewVBox(statusRow, u.progress))

	// —— 下载目录 ——
	u.dlDirLbl = newMutedLabel(u.downloadDir, false)
	u.dlDirLbl.Truncation = fyne.TextTruncateEllipsis
	chooseDlBtn := widget.NewButtonWithIcon("更改", theme.FolderIcon(), u.chooseDownloadDir)
	chooseDlBtn.Importance = widget.LowImportance
	dlRow := container.NewBorder(nil, nil,
		container.NewHBox(newCenteredIcon(theme.DownloadIcon(), 18), widget.NewLabel("下载至")),
		chooseDlBtn, u.dlDirLbl,
	)
	dlCard := newPanel("", dlRow)

	bottom := container.NewVBox(buttons, statusCard, dlCard)

	body := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		bottom, nil, nil,
		deviceCard,
	)
	return container.NewPadded(body)
}

// buildHeader 构造顶部品牌头：左侧图标 + 名称 + slogan，右侧本机身份胶囊。
func (u *ui) buildHeader(name string, self protocol.DeviceInfo) fyne.CanvasObject {
	logo := canvas.NewImageFromResource(fyne.NewStaticResource("icon.png", appIcon))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(40, 40))

	brand := canvas.NewText("HopDrop", theme.Color(theme.ColorNamePrimary))
	brand.TextSize = 26
	brand.TextStyle = fyne.TextStyle{Bold: true}
	slogan := newMutedLabel("局域网 · 极速互传", false)

	left := container.NewHBox(logo, container.NewVBox(brand, slogan))

	// 本机身份胶囊：设备名 + 平台 + 可直连端点（供手动配对展示）。
	selfIcon := newCenteredIcon(platformIcon(self.Platform), 18)
	selfName := widget.NewLabelWithStyle(name, fyne.TextAlign(0), fyne.TextStyle{Bold: true})
	selfPlat := newMutedLabel("本机 · "+string(self.Platform)+" · "+u.localEndpoint(), false)
	selfInfo := container.NewHBox(selfIcon, container.NewVBox(selfName, selfPlat))
	right := newPanel("", selfInfo)

	return container.NewBorder(nil, nil, left, right, nil)
}

// localEndpoint 返回本机首个可直连的 "host:port"（供手动配对展示）；无地址时返回占位。
func (u *ui) localEndpoint() string {
	ips := discovery.LocalAddresses()
	if len(ips) == 0 {
		return "无局域网地址"
	}
	return net.JoinHostPort(ips[0], fmt.Sprint(u.node.Self().SyncPort))
}

// pairingURI 返回本机配对串，供二维码编码。带上 id 便于扫码方把本机登记为在线设备。
func (u *ui) pairingURI() string {
	ips := discovery.LocalAddresses()
	if len(ips) == 0 {
		return ""
	}
	self := u.node.Self()
	return fmt.Sprintf("hopdrop://%s?id=%s&name=%s&platform=%s&fingerprint=%s",
		net.JoinHostPort(ips[0], fmt.Sprint(self.SyncPort)), url.QueryEscape(self.ID), url.QueryEscape(self.Name), self.Platform,
		url.QueryEscape(self.Fingerprint))
}

// refreshPeers 从 node 拉取最新在线设备并刷新列表（在 UI 线程安全地更新）。
func (u *ui) refreshPeers() {
	peers := u.node.Peers()
	sort.Slice(peers, func(i, j int) bool { return peers[i].Device.Name < peers[j].Device.Name })
	u.mu.Lock()
	u.peers = peers
	n := len(peers)
	u.mu.Unlock()
	fyne.Do(func() {
		u.peerList.Refresh()
		if u.peerCount != nil {
			u.peerCount.SetText(fmt.Sprintf("在线设备 (%d)", n))
		}
		if u.emptyHint != nil {
			if n > 0 {
				u.emptyHint.Hide()
			} else {
				u.emptyHint.Show()
			}
		}
	})
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
		u.setStatus(fmt.Sprintf("正在发送到 %s …", target.Device.Name), statusBusy)
		u.progress.SetValue(0)
		u.progress.Show()
	})
	go func() {
		err := u.node.SendPaths(context.Background(), target, paths)
		fyne.Do(func() {
			if err != nil {
				u.progress.Hide()
				dialog.ShowError(err, u.win)
				u.setStatus("发送失败", statusError)
			}
		})
	}()
}

// onDecision 在后台收到 Offer 时被调用；这里同步弹窗等待用户点击接受/拒绝。
func (u *ui) onDecision(ctx context.Context, peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision {
	ch := make(chan bool, 1)
	msg := fmt.Sprintf("%s 想给你发送 %d 个文件（共 %s）。\n是否接收到:\n%s ？",
		peer.Name, len(offer.Files), humanBytes(offer.TotalBytes), u.downloadDir)
	fyne.Do(func() {
		dialog.ShowConfirm("收到文件传输", msg, func(ok bool) { ch <- ok }, u.win)
	})
	select {
	case accept := <-ch:
		return protocol.Decision{Accept: accept}
	case <-ctx.Done():
		return protocol.Decision{Accept: false, Reason: "request canceled"}
	}
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
			u.setStatus(fmt.Sprintf("%s %d/%d · %s (%s/%s)",
				verb, p.Files, p.TotalFiles, p.CurrentName, humanBytes(p.Bytes), humanBytes(p.TotalBytes)), statusBusy)
		case "done":
			u.progress.SetValue(1)
			u.progress.Hide()
			verb := "接收"
			if p.Direction == "send" {
				verb = "发送"
			}
			u.setStatus(fmt.Sprintf("%s完成 · %d 个文件 · 共 %s", verb, p.Files, humanBytes(p.Bytes)), statusOK)
		case "rejected":
			u.progress.Hide()
			u.setStatus("对方拒绝了本次传输", statusWarn)
		case "error":
			u.progress.Hide()
			u.setStatus("出错: "+p.Err, statusError)
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
		u.node = syncpkg.NewNode(self.Name, self.Platform, u.node.Identity(), sink)
		u.node.OnPeer(func(ev discovery.PeerEvent) { u.refreshPeers() })
		u.node.OnDecision(u.onDecision)
		u.node.OnProgress(u.onProgress)
		_ = u.startNode()
		u.dlDirLbl.SetText(newDir)
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

func desktopIdentityPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "HopDrop", "identity-v1.json")
	}
	return filepath.Join(os.TempDir(), "HopDrop", "identity-v1.json")
}

func deviceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "HopDrop-Desktop"
}
