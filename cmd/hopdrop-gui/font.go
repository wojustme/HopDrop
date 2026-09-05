package main

import (
	"os"
	"runtime"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// cjkFontOnce/cjkFontRes 缓存一次性加载的中日韩字体资源。
var (
	cjkFontOnce sync.Once
	cjkFontRes  fyne.Resource
)

// cjkFontCandidates 按平台给出「单一 TTF」的候选路径。
//
// 为什么必须是单一 TTF：Fyne 2.8 的字体管线对 theme.Font() 返回的资源调用
// font.ParseTTF，而它显式拒绝 TTC 字体集合（"collections not allowed"）。
// 系统里的中文黑体多为 .ttc（PingFang / STHeiti / msyh），无法直接用；
// 因此这里优先选择系统自带、覆盖中英文的单体 .ttf。
func cjkFontCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			// Arial Unicode 覆盖中英文且是单体 TTF，能保证所有字形来自同一个 face，
			// 从根源消除「中文逐字回退到不同字体 → 基线抖动（波浪）」的问题。
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/Library/Fonts/Arial Unicode.ttf",
		}
	case "windows":
		// Windows 上多数中文黑体是 .ttc 集合（msyh.ttc / simsun.ttc），Fyne 会拒绝；
		// 这里优先选覆盖中英文的单体 .ttf：DengXian（Win10+ 自带）> 旧版雅黑 > 微软正黑。
		return []string{
			`C:\Windows\Fonts\Deng.ttf`,     // 等线 DengXian Regular（单体 TTF）
			`C:\Windows\Fonts\Dengl.ttf`,    // 等线 Light
			`C:\Windows\Fonts\msyh.ttf`,     // 旧版微软雅黑（部分系统为单体 TTF）
			`C:\Windows\Fonts\msjh.ttf`,     // 微软正黑
			`C:\Windows\Fonts\arialuni.ttf`, // Arial Unicode（若安装了 Office）
		}
	default: // linux 等
		return []string{
			"/usr/share/fonts/truetype/arphic/uming.ttf",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttf",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttf",
		}
	}
}

// loadCJKFont 尝试加载一个覆盖中英文的单体字体资源；找不到时返回 nil。
// 加载成功后，主题会把它用于所有文本，使中英文共用同一 face、基线一致，
// 从而让文字保持水平排版而非逐字上下抖动。
func loadCJKFont() fyne.Resource {
	cjkFontOnce.Do(func() {
		for _, p := range cjkFontCandidates() {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if res, err := fyne.LoadResourceFromPath(p); err == nil {
				cjkFontRes = res
				return
			}
		}
	})
	return cjkFontRes
}

// themeFont 返回主题应使用的字体：优先中英文统一字体，否则回退到内置字体。
func themeFont(style fyne.TextStyle) fyne.Resource {
	if res := loadCJKFont(); res != nil {
		return res
	}
	return theme.DefaultTheme().Font(style)
}
