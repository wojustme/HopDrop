package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// hopTheme 是 HopDrop 桌面端的自定义主题，主打「米白 + 科技感」：
//   - 以温润的米白（cream / off-white）为整体背景，卡片用更亮的暖白拉出层次；
//   - 深墨蓝作为主文字色、深青（teal）作为强调色，在米白底上对比清晰、冷暖平衡；
//   - 更大的圆角与内边距，让卡片、按钮更现代通透。
//
// 它实现 fyne.Theme 的四个方法（Color/Font/Icon/Size），未特化的项回退到内置主题，
// 保证在不同 Fyne 版本上都有合理默认值。
//
// forceLight 为 true 时无视系统明暗偏好，始终使用米白配色——主视觉以米白为准，
// 强制亮色可保证在深色系统下依旧保持一致的观感。
type hopTheme struct {
	forceLight bool
}

var _ fyne.Theme = hopTheme{}

// —— 米白科技调色板 ——
// 以米白为背景层，深青为强调色，深墨蓝为文字，冷暖搭配呈现干净的科技感。
var (
	// 背景层：窗口底（米白）< 卡片面板（暖白）；输入框略深于窗口底，靠明度层次拉开纵深。
	creamBackground = color.NRGBA{R: 0xF5, G: 0xF1, B: 0xE7, A: 0xFF} // 米白（窗口底）
	creamSurface    = color.NRGBA{R: 0xFC, G: 0xFA, B: 0xF4, A: 0xFF} // 卡片 / 面板（暖白）
	creamInput      = color.NRGBA{R: 0xEE, G: 0xE8, B: 0xD9, A: 0xFF} // 输入框 / 按钮底
	creamOverlay    = color.NRGBA{R: 0xFD, G: 0xFB, B: 0xF6, A: 0xFF} // 对话框背景

	// 主色与前景。
	accentTeal      = color.NRGBA{R: 0x0C, G: 0x8F, B: 0xA6, A: 0xFF} // 深青（主色），米白上对比清晰
	accentTealSoft  = color.NRGBA{R: 0x0C, G: 0x8F, B: 0xA6, A: 0x30} // 主色半透明（focus / 选择）
	accentTealHover = color.NRGBA{R: 0x0C, G: 0x8F, B: 0xA6, A: 0x18} // 悬浮
	accentTealPress = color.NRGBA{R: 0x0C, G: 0x8F, B: 0xA6, A: 0x40} // 按下
	creamForeground = color.NRGBA{R: 0x24, G: 0x2A, B: 0x33, A: 0xFF} // 主文字：深墨蓝灰
	creamPlaceHold  = color.NRGBA{R: 0x8C, G: 0x86, B: 0x76, A: 0xFF} // 占位 / 次要文字：暖灰
	creamDisabled   = color.NRGBA{R: 0xB8, G: 0xB2, B: 0xA2, A: 0xFF}
	creamSeparator  = color.NRGBA{R: 0xE2, G: 0xDB, B: 0xC9, A: 0xFF} // 分隔线：比背景略深的暖调
	creamScrollBar  = color.NRGBA{R: 0xC7, G: 0xBF, B: 0xAC, A: 0xCC}

	// 语义色（成功 / 警告 / 危险）：在米白底上取中高饱和度，保证辨识度又不刺眼。
	creamSuccess = color.NRGBA{R: 0x12, G: 0xA4, B: 0x6E, A: 0xFF} // 森野绿
	creamWarning = color.NRGBA{R: 0xC9, G: 0x8A, B: 0x00, A: 0xFF} // 琥珀
	creamError   = color.NRGBA{R: 0xD6, G: 0x3B, B: 0x5A, A: 0xFF} // 品红红

	// 暗色变体：保留一套深色配色，深色系统 / 需要时可切换，同样使用青色主色以保持品牌一致。
	darkBackground = color.NRGBA{R: 0x14, G: 0x18, B: 0x1E, A: 0xFF}
	darkSurface    = color.NRGBA{R: 0x1C, G: 0x21, B: 0x2A, A: 0xFF}
	darkInput      = color.NRGBA{R: 0x25, G: 0x2C, B: 0x38, A: 0xFF}
	darkForeground = color.NRGBA{R: 0xEC, G: 0xEA, B: 0xE2, A: 0xFF}
	darkPlaceHold  = color.NRGBA{R: 0x8A, G: 0x90, B: 0x9C, A: 0xFF}
	darkSeparator  = color.NRGBA{R: 0x2E, G: 0x35, B: 0x42, A: 0xFF}
	accentTealDark = color.NRGBA{R: 0x2A, G: 0xC7, B: 0xDE, A: 0xFF} // 深色下更亮的青
)

// Color 返回给定语义色在指定明暗变体下的颜色。
func (t hopTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.forceLight || variant == theme.VariantLight {
		return creamColor(name)
	}
	return darkColor(name)
}

// creamColor 返回米白主题下各语义色的取值。
func creamColor(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return creamBackground
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return creamInput
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground:
		return creamSurface
	case theme.ColorNameOverlayBackground:
		return creamOverlay
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return accentTeal
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 0xFC, G: 0xFA, B: 0xF4, A: 0xFF} // 青色按钮上用暖白文字
	case theme.ColorNameForeground:
		return creamForeground
	case theme.ColorNamePlaceHolder:
		return creamPlaceHold
	case theme.ColorNameDisabled, theme.ColorNameDisabledButton:
		return creamDisabled
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return creamSeparator
	case theme.ColorNameHover:
		return accentTealHover
	case theme.ColorNamePressed:
		return accentTealPress
	case theme.ColorNameFocus, theme.ColorNameSelection:
		return accentTealSoft
	case theme.ColorNameScrollBar:
		return creamScrollBar
	case theme.ColorNameSuccess:
		return creamSuccess
	case theme.ColorNameWarning:
		return creamWarning
	case theme.ColorNameError:
		return creamError
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0x3A, G: 0x33, B: 0x22, A: 0x33} // 暖调浅阴影
	}
	return theme.LightTheme().Color(name, theme.VariantLight)
}

// darkColor 返回暗色主题下各语义色的取值（备用）。
func darkColor(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return darkBackground
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return darkInput
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return darkSurface
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return accentTealDark
	case theme.ColorNameForegroundOnPrimary:
		return darkBackground
	case theme.ColorNameForeground:
		return darkForeground
	case theme.ColorNamePlaceHolder:
		return darkPlaceHold
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return darkSeparator
	case theme.ColorNameFocus, theme.ColorNameSelection:
		return color.NRGBA{R: 0x2A, G: 0xC7, B: 0xDE, A: 0x33}
	case theme.ColorNameHover:
		return color.NRGBA{R: 0x2A, G: 0xC7, B: 0xDE, A: 0x1F}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x2E, G: 0xF2, B: 0xA0, A: 0xFF}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xFF, G: 0xC4, B: 0x4D, A: 0xFF}
	case theme.ColorNameError:
		return color.NRGBA{R: 0xFF, G: 0x4D, B: 0x7A, A: 0xFF}
	}
	return theme.DarkTheme().Color(name, theme.VariantDark)
}

// Font 返回文本字体。为避免中文逐字回退到不同字体导致基线抖动（文字呈波浪状），
// 优先使用一个覆盖中英文的单体字体，让所有字形来自同一 face、水平对齐；
// 找不到时回退内置字体。
func (hopTheme) Font(style fyne.TextStyle) fyne.Resource { return themeFont(style) }

// Icon 沿用内置图标资源。
func (hopTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(name) }

// Size 覆盖若干尺寸，增大圆角与留白以呈现更通透、更现代的科技感。
func (hopTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameButtonRadius:
		return 10
	case theme.SizeNameInputRadius:
		return 10
	case theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameCardRadius:
		return 14
	case theme.SizeNameDialogRadius:
		return 16
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarRadius:
		return 5
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 26
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return theme.DefaultTheme().Size(name)
}
