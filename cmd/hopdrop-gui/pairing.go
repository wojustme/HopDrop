package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// showPairing 弹出手动配对面板：展示本机二维码 + 直连端点，并提供「按端点发送」入口。
//
// Bonjour 不可用时，手动配对可绕过发现、直连 HTTPS 传输：
//   - 对端扫本机二维码即可向本机发送；
//   - 本机粘贴对端完整配对串亦可主动发送，并固定校验其 TLS 指纹。
func (u *ui) showPairing() {
	endpoint := u.localEndpoint()
	uri := u.pairingURI()

	// 本机二维码。
	var qrObj fyne.CanvasObject
	if img := qrImage(uri, 220); img != nil {
		qrCanvas := canvas.NewImageFromImage(img)
		qrCanvas.FillMode = canvas.ImageFillContain
		qrCanvas.SetMinSize(fyne.NewSize(220, 220))
		qrObj = qrCanvas
	} else {
		qrObj = container.NewCenter(newMutedLabel("无局域网地址，请检查网络连接", false))
	}

	epLabel := widget.NewLabelWithStyle(endpoint, fyne.TextAlign(1), fyne.TextStyle{Bold: true, Monospace: true})

	// 粘贴完整配对串，端点和 TLS 指纹会一起校验。
	epEntry := widget.NewEntry()
	epEntry.SetPlaceHolder("hopdrop://… 配对串")
	sendBtn := widget.NewButtonWithIcon("选文件发送", theme.MailSendIcon(), func() {
		endpoint, fingerprint, parseErr := directTarget(epEntry.Text)
		if parseErr != nil {
			dialog.ShowError(parseErr, u.win)
			return
		}
		dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			path := rc.URI().Path()
			rc.Close()
			u.startSendEndpoint(endpoint, fingerprint, []string{path})
		}, u.win)
	})
	sendBtn.Importance = widget.HighImportance

	// 用 NewCustomWithoutButtons 自绘布局：内容区可滚动，底部单独放一行“关闭”按钮，
	// 避免默认对话框把关闭按钮挤在高内容下方错位。
	var d dialog.Dialog

	body := container.NewVBox(
		widget.NewLabelWithStyle("用手机 HopDrop 扫这个码即可连接", fyne.TextAlign(1), fyne.TextStyle{Bold: true}),
		container.NewCenter(qrObj),
		epLabel,
		newMutedLabel("扫码后本机会出现在手机的设备列表里，可直接互传。", false),
		widget.NewSeparator(),
		newMutedLabel("或粘贴对方的完整配对串后发送：", false),
		epEntry,
		sendBtn,
	)

	closeBtn := widget.NewButton("关闭", func() {
		if d != nil {
			d.Hide()
		}
	})
	// 底部工具条：右对齐的关闭按钮，与内容用分隔线隔开，布局清爽不拥挤。
	footer := container.NewBorder(widget.NewSeparator(), nil, nil, nil,
		container.NewHBox(layout.NewSpacer(), closeBtn),
	)

	content := container.NewBorder(
		nil, footer, nil, nil,
		container.NewVScroll(container.NewPadded(body)),
	)

	d = dialog.NewCustomWithoutButtons("手动配对", content, u.win)
	d.Resize(fyne.NewSize(360, 560))
	d.Show()
}

// startSendEndpoint 向一个 "host:port" 直连端点发送（不依赖发现记录）。
func (u *ui) startSendEndpoint(endpoint, fingerprint string, paths []string) {
	fyne.Do(func() {
		u.setStatus("正在直连发送到 "+endpoint+" …", statusBusy)
		u.progress.SetValue(0)
		u.progress.Show()
	})
	go func() {
		err := u.node.SendToEndpoint(context.Background(), endpoint, fingerprint, paths)
		fyne.Do(func() {
			if err != nil {
				u.progress.Hide()
				dialog.ShowError(err, u.win)
				u.setStatus("发送失败", statusError)
			}
		})
	}()
}

func directTarget(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "hopdrop" || u.Host == "" {
		return "", "", fmt.Errorf("请输入完整的 hopdrop:// 配对串")
	}
	fingerprint := u.Query().Get("fingerprint")
	if fingerprint == "" {
		return "", "", fmt.Errorf("配对串缺少设备指纹")
	}
	return u.Host, fingerprint, nil
}

// qrImage 用 boombuler/barcode 生成一张 size×size 的黑白二维码图片；content 为空返回 nil。
func qrImage(content string, size int) image.Image {
	if content == "" {
		return nil
	}
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return nil
	}
	scaled, err := barcode.Scale(code, size, size)
	if err != nil {
		return nil
	}
	// barcode.Scale 已返回 image.Image；确保白底（部分渲染器对透明处理不一）。
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r, g, b, a := scaled.At(x, y).RGBA()
			if a == 0 {
				out.Set(x, y, color.White)
			} else {
				out.Set(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xFF})
			}
		}
	}
	return out
}
