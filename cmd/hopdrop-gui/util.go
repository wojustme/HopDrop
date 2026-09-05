package main

import (
	"fmt"
	"image/color"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// currentPlatform 把 Go 的 GOOS 映射成协议里的 Platform 常量。
func currentPlatform() protocol.Platform {
	switch runtime.GOOS {
	case "darwin":
		return protocol.PlatformMacOS
	case "windows":
		return protocol.PlatformWindows
	case "linux":
		return protocol.PlatformLinux
	default:
		return protocol.PlatformUnknown
	}
}

// humanBytes 把字节数格式化成人类可读的字符串（如 "1.2 MB"）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// —— 状态灯 ——

// statusKind 描述状态条指示灯的语义，用于映射到主题里的颜色。
type statusKind int

const (
	statusIdle  statusKind = iota // 空闲（灰）
	statusBusy                    // 传输中（青，主色）
	statusOK                      // 成功（绿）
	statusWarn                    // 警告 / 被拒（黄）
	statusError                   // 失败（红）
)

// setStatus 更新底部状态文案与指示灯颜色（需在 UI 线程调用）。
func (u *ui) setStatus(text string, kind statusKind) {
	u.statusLbl.SetText(text)
	if u.statusDot == nil {
		return
	}
	var c color.Color
	switch kind {
	case statusBusy:
		c = theme.Color(theme.ColorNamePrimary)
	case statusOK:
		c = theme.Color(theme.ColorNameSuccess)
	case statusWarn:
		c = theme.Color(theme.ColorNameWarning)
	case statusError:
		c = theme.Color(theme.ColorNameError)
	default:
		c = theme.Color(theme.ColorNamePlaceHolder)
	}
	u.statusDot.FillColor = c
	u.statusDot.Refresh()
}

// —— 通用视觉辅助 ——

// platformIcon 依据平台返回一个更贴切的内置图标。
func platformIcon(p protocol.Platform) fyne.Resource {
	switch p {
	case protocol.PlatformAndroid, protocol.PlatformIOS:
		return theme.MediaPhotoIcon()
	default:
		return theme.ComputerIcon()
	}
}

// newCenteredIcon 生成一个定尺寸、垂直居中的图标控件。
func newCenteredIcon(res fyne.Resource, size float32) fyne.CanvasObject {
	ic := widget.NewIcon(res)
	return container.NewCenter(container.NewGridWrap(fyne.NewSize(size, size), ic))
}

// newMutedLabel 生成一个弱化（占位色）的说明文字，small 为 true 时用更小字号。
func newMutedLabel(text string, heading bool) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	if heading {
		l.TextStyle = fyne.TextStyle{Bold: true}
	} else {
		l.SizeName = theme.SizeNameCaptionText
	}
	return l
}

// newPanel 用一层带主题背景色和圆角的矩形包裹内容，形成「面板 / 卡片」观感。
// title 非空时在顶部加一行加粗标题。
func newPanel(title string, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameHeaderBackground))
	bg.CornerRadius = theme.Size(theme.SizeNameCardRadius)
	bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	bg.StrokeWidth = 1

	inner := content
	if title != "" {
		inner = container.NewVBox(
			widget.NewLabelWithStyle(title, fyne.TextAlign(0), fyne.TextStyle{Bold: true}),
			content,
		)
	}
	return container.NewStack(bg, container.NewPadded(inner))
}

// —— 自定义设备行 ——

// peerRow 是设备列表里的一行：状态灯 + 平台图标 + 名字/端点 + 平台标签。
type peerRow struct {
	widget.BaseWidget
	dot      *canvas.Circle
	icon     *widget.Icon
	name     *widget.Label
	endpoint *widget.Label
	plat     *widget.Label
}

func newPeerRow() *peerRow {
	r := &peerRow{
		dot:      canvas.NewCircle(theme.Color(theme.ColorNameSuccess)),
		icon:     widget.NewIcon(theme.ComputerIcon()),
		name:     widget.NewLabelWithStyle("", fyne.TextAlign(0), fyne.TextStyle{Bold: true}),
		endpoint: newMutedLabel("", false),
		plat:     newMutedLabel("", false),
	}
	r.ExtendBaseWidget(r)
	return r
}

// set 用一个 peer 的信息填充该行。
func (r *peerRow) set(p discovery.Peer) {
	r.name.SetText(p.Device.Name)
	r.endpoint.SetText(p.SyncEndpoint())
	r.plat.SetText(string(p.Device.Platform))
	r.icon.SetResource(platformIcon(p.Device.Platform))
	r.dot.FillColor = theme.Color(theme.ColorNameSuccess)
	r.dot.Refresh()
}

func (r *peerRow) CreateRenderer() fyne.WidgetRenderer {
	dotWrap := container.NewCenter(container.NewGridWrap(fyne.NewSize(9, 9), r.dot))
	iconWrap := container.NewCenter(container.NewGridWrap(fyne.NewSize(22, 22), r.icon))
	left := container.NewHBox(dotWrap, iconWrap)
	center := container.NewVBox(r.name, r.endpoint)
	platTag := container.NewCenter(r.plat)
	row := container.NewBorder(nil, nil, left, platTag, center)
	return widget.NewSimpleRenderer(container.NewPadded(row))
}
