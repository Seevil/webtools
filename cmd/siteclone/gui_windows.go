//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"siteclone/internal/clone"
)

const (
	idURL = 101 + iota
	idDomains
	idInclude
	idExclude
	idOutput
	idDepth
	idPages
	idAssets
	idTimeout
	idDelay
	idFileMB
	idTotalMB
	idCookie
	idUA
	idReferer
	idExternal
	idAuthorize
	idStart
	idCancel
	idFolder
	idImportURL
	idSaveConfig
	idLoadConfig
	idPreview
	idOpenOutput
	idZIP
	idHelp
	idLog
	idStatus
	idStats
)

type placement struct {
	hwnd       uintptr
	x, y, w, h int
	stretch    bool
}
type completion struct {
	result      clone.Result
	err         error
	export      bool
	destination string
}
type application struct {
	hwnd, instance, font, titleFont, brush uintptr
	controls                               map[int]uintptr
	positions                              []placement
	events                                 chan clone.Event
	finished                               chan completion
	cancel                                 context.CancelFunc
	result                                 clone.Result
	preview                                *clone.Preview
	busy                                   bool
	exporting                              bool
	lines                                  []string
	started                                time.Time
	last                                   clone.Event
	width, height, scroll                  int
	scale                                  float64
}

var app *application

type scrollInfo struct {
	Size, Mask    uint32
	Min, Max      int32
	Page          uint32
	Pos, TrackPos int32
}

func (a *application) px(n int) int32 { return int32(float64(n) * a.scale) }
func (a *application) add(id int, class, value string, style uint32, x, y, w, h int, stretch bool) uintptr {
	ex := uintptr(0)
	if class == "EDIT" {
		ex = 0x200
		style |= 0x10000
	}
	if class == "BUTTON" {
		style |= 0x10000
	}
	hwnd, _, _ := createWindow.Call(ex, uintptr(unsafe.Pointer(wide(class))), uintptr(unsafe.Pointer(wide(value))), uintptr(0x50000000|style), signed(a.px(x)), signed(a.px(y)), signed(a.px(w)), signed(a.px(h)), a.hwnd, uintptr(id), a.instance, 0)
	if hwnd == 0 {
		panic("无法创建 Windows 控件")
	}
	sendMessage.Call(hwnd, 0x30, a.font, 1)
	if id != 0 {
		a.controls[id] = hwnd
	}
	a.positions = append(a.positions, placement{hwnd, x, y, w, h, stretch})
	return hwnd
}
func (a *application) label(s string, x, y, w int) { a.add(0, "STATIC", s, 0, x, y, w, 24, false) }
func (a *application) edit(id int, value string, x, y, w int, stretch bool) {
	h := a.add(id, "EDIT", value, 0x80, x, y, w, 28, stretch)
	sendMessage.Call(h, 0xC5, 8192, 0)
}
func (a *application) button(id int, label string, x, y, w int) {
	a.add(id, "BUTTON", label, 0, x, y, w, 30, false)
}
func (a *application) value(id int) string { return strings.TrimSpace(text(a.controls[id])) }
func (a *application) checked(id int) bool {
	v, _, _ := sendMessage.Call(a.controls[id], 0xF0, 0, 0)
	return v == 1
}
func (a *application) check(id int, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	sendMessage.Call(a.controls[id], 0xF1, v, 0)
}

func (a *application) createControls() {
	header := a.add(0, "STATIC", "网站克隆工具", 0, 26, 18, 430, 36, false)
	sendMessage.Call(header, 0x30, a.titleFont, 1)
	a.label("GO NATIVE  /  本地桌面工作台", 26, 58, 500)
	a.button(idLoadConfig, "导入配置", 700, 28, 105)
	a.button(idSaveConfig, "保存配置", 819, 28, 105)
	a.label("01  单站下载", 26, 99, 500)
	rows := []struct {
		id    int
		label string
		y     int
	}{
		{idURL, "下载网址", 135}, {idDomains, "限定域名", 173}, {idInclude, "限定路径", 211},
		{idExclude, "排除路径", 249}, {idOutput, "保存目录", 287},
	}
	for _, r := range rows {
		a.label(r.label, 32, r.y+4, 90)
		w := 792
		if r.id == idURL || r.id == idOutput {
			w = 674
		}
		a.edit(r.id, "", 132, r.y, w, true)
	}
	a.button(idImportURL, "导入网址", 819, 135, 105)
	a.button(idFolder, "选择文件夹", 819, 287, 105)
	a.label("域名以分号分隔；支持 *.example.org。路径按前缀匹配，留空表示不限。", 132, 320, 790)
	items := []struct {
		id    int
		label string
		x, y  int
	}{
		{idDepth, "最大深度", 32, 359}, {idPages, "最大页数", 338, 359}, {idAssets, "资源上限", 644, 359},
		{idTimeout, "超时 / 秒", 32, 397}, {idDelay, "间隔 / 毫秒", 338, 397}, {idFileMB, "单文件 / MB", 644, 397},
	}
	for _, v := range items {
		a.label(v.label, v.x, v.y+4, 105)
		a.edit(v.id, "", v.x+115, v.y, 145, false)
	}
	a.label("总量 / MB", 32, 439, 105)
	a.edit(idTotalMB, "", 147, 435, 145, false)
	a.add(idExternal, "BUTTON", "允许下载跨域资源（CDN、字体、图片）", 3, 338, 435, 570, 28, false)
	a.label("02  请求身份", 26, 480, 500)
	a.label("Cookie", 32, 519, 90)
	a.edit(idCookie, "", 132, 515, 792, true)
	sendMessage.Call(a.controls[idCookie], 0xCC, uintptr('*'), 0)
	a.label("User-Agent", 32, 557, 100)
	a.edit(idUA, "", 132, 553, 792, true)
	a.label("来源网址", 32, 595, 90)
	a.edit(idReferer, "", 132, 591, 792, true)
	a.label("Cookie 仅保留在内存，不写入配置或日志；只发送到起始网址同源请求。", 132, 628, 790)
	a.add(idAuthorize, "BUTTON", "我拥有目标站点或已获授权；了解动态接口与服务端业务不能被复制。", 3, 32, 666, 895, 28, false)
	a.button(idStart, "开始下载", 32, 708, 130)
	a.button(idCancel, "取消任务", 176, 708, 120)
	a.button(idPreview, "本地预览", 460, 708, 112)
	a.button(idOpenOutput, "打开目录", 584, 708, 112)
	a.button(idZIP, "导出 ZIP", 708, 708, 104)
	a.button(idHelp, "使用说明", 824, 708, 100)
	a.add(idStatus, "STATIC", "就绪 · 请填写网址并确认授权", 0, 32, 753, 892, 25, true)
	a.add(idLog, "EDIT", "", 0x4|0x40|0x800|0x200000, 32, 788, 892, 145, true)
	sendMessage.Call(a.controls[idLog], 0xC5, 100000, 0)
	a.add(idStats, "STATIC", "页面 0  |  资源 0  |  失败 0  |  下载 0 KB  |  用时 00:00", 0, 32, 948, 892, 28, true)
	a.applyConfig(clone.DefaultConfig())
	a.appendLog("仅克隆前端静态资源；不会执行脚本发现动态内容，也不会自动读取浏览器凭据。")
	a.setBusy(false)
}

func (a *application) layout() {
	if len(a.positions) == 0 {
		return
	}
	var r rect
	user32.NewProc("GetClientRect").Call(a.hwnd, uintptr(unsafe.Pointer(&r)))
	a.width = int(float64(r.Right) / a.scale)
	a.height = int(float64(r.Bottom) / a.scale)
	max := 996 - a.height
	if max < 0 {
		max = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
	if a.scroll < 0 {
		a.scroll = 0
	}
	si := scrollInfo{Mask: 0x7, Min: 0, Max: 995, Page: uint32(a.height), Pos: int32(a.scroll)}
	si.Size = uint32(unsafe.Sizeof(si))
	user32.NewProc("SetScrollInfo").Call(a.hwnd, 1, uintptr(unsafe.Pointer(&si)), 1)
	for _, p := range a.positions {
		w := p.w
		x := p.x
		if p.stretch {
			w += a.width - 958
		} else if p.x >= 819 || (p.y == 28 && p.x >= 700) {
			x += a.width - 958
		}
		moveWindow.Call(p.hwnd, signed(a.px(x)), signed(a.px(p.y-a.scroll)), signed(a.px(w)), signed(a.px(p.h)), 1)
	}
	user32.NewProc("InvalidateRect").Call(a.hwnd, 0, 1)
}

func (a *application) applyConfig(c clone.Config) {
	stringsByID := map[int]string{idURL: c.URL, idDomains: c.Domains, idInclude: c.Include, idExclude: c.Exclude, idOutput: c.Output, idUA: c.UserAgent, idReferer: c.Referer, idCookie: ""}
	for id, v := range stringsByID {
		setText(a.controls[id], v)
	}
	ints := map[int]int{idDepth: c.MaxDepth, idPages: c.MaxPages, idAssets: c.MaxAssets, idTimeout: c.TimeoutSeconds, idDelay: c.DelayMillis, idFileMB: c.MaxFileMB, idTotalMB: c.MaxTotalMB}
	for id, v := range ints {
		setText(a.controls[id], strconv.Itoa(v))
	}
	a.check(idExternal, c.ExternalAssets)
}

func (a *application) config() (clone.Config, error) {
	c := clone.DefaultConfig()
	c.URL = a.value(idURL)
	c.Domains = a.value(idDomains)
	c.Include = a.value(idInclude)
	c.Exclude = a.value(idExclude)
	c.Output = a.value(idOutput)
	c.Cookie = a.value(idCookie)
	c.UserAgent = a.value(idUA)
	c.Referer = a.value(idReferer)
	c.ExternalAssets = a.checked(idExternal)
	for id, p := range map[int]*int{idDepth: &c.MaxDepth, idPages: &c.MaxPages, idAssets: &c.MaxAssets, idTimeout: &c.TimeoutSeconds, idDelay: &c.DelayMillis, idFileMB: &c.MaxFileMB, idTotalMB: &c.MaxTotalMB} {
		n, e := strconv.Atoi(a.value(id))
		if e != nil {
			return c, errors.New("所有数值设置必须是整数")
		}
		*p = n
	}
	_, err := c.Validate()
	return c, err
}

func (a *application) appendLog(s string) {
	a.lines = append(a.lines, time.Now().Format("15:04:05")+"  "+s)
	if len(a.lines) > 220 {
		a.lines = a.lines[len(a.lines)-220:]
	}
	setText(a.controls[idLog], strings.Join(a.lines, "\r\n"))
	sendMessage.Call(a.controls[idLog], 0xB1, ^uintptr(0), ^uintptr(0))
	sendMessage.Call(a.controls[idLog], 0xB7, 0, 0)
}
func (a *application) setBusy(on bool) {
	a.busy = on
	for id := idURL; id <= idAuthorize; id++ {
		enabled(a.controls[id], !on)
	}
	for _, id := range []int{idStart, idFolder, idImportURL, idSaveConfig, idLoadConfig} {
		enabled(a.controls[id], !on)
	}
	enabled(a.controls[idCancel], on && !a.exporting)
	for _, id := range []int{idPreview, idOpenOutput, idZIP} {
		enabled(a.controls[id], !on && a.result.Entry != "")
	}
}
func (a *application) fail(err error) {
	if err != nil {
		messageBox(a.hwnd, "提示", err.Error(), 0x30)
	}
}

func (a *application) start() {
	if a.busy {
		return
	}
	if !a.checked(idAuthorize) {
		a.fail(errors.New("请先确认拥有目标网站或已获得复制授权"))
		return
	}
	c, err := a.config()
	if err != nil {
		a.fail(err)
		return
	}
	if a.preview != nil {
		a.preview.Close()
		a.preview = nil
	}
	a.result = clone.Result{}
	a.lines = nil
	a.last = clone.Event{}
	a.started = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.setBusy(true)
	setText(a.controls[idStatus], "下载中 · 队列会随页面发现而变化")
	go func() {
		defer cancel()
		r, e := clone.Run(ctx, c, func(event clone.Event) {
			select {
			case a.events <- event:
			default:
			}
		})
		a.finished <- completion{result: r, err: e}
	}()
}

func (a *application) tick() {
	for {
		select {
		case e := <-a.events:
			a.last = e
			a.appendLog(e.Message)
		default:
			goto drained
		}
	}
drained:
	if a.busy && !a.exporting {
		e := a.last
		elapsed := int(time.Since(a.started).Seconds())
		setText(a.controls[idStats], fmt.Sprintf("页面 %d  |  资源 %d  |  失败 %d  |  待处理 %d  |  下载 %.1f KB  |  用时 %02d:%02d", e.Pages, e.Assets, e.Failed, e.Pending, float64(e.Bytes)/1024, elapsed/60, elapsed%60))
	}
	select {
	case done := <-a.finished:
		a.cancel = nil
		a.exporting = false
		if done.export {
			a.setBusy(false)
			if done.err != nil {
				a.appendLog("ZIP 导出失败；如生成了部分文件，请手动处理。")
				a.fail(done.err)
				setText(a.controls[idStatus], "导出失败")
			} else {
				a.appendLog("ZIP 已保存：" + done.destination)
				setText(a.controls[idStatus], "ZIP 导出完成")
			}
			return
		}
		for {
			select {
			case e := <-a.events:
				a.last = e
				a.appendLog(e.Message)
			default:
				goto completed
			}
		}
	completed:
		a.result = done.result
		a.setBusy(false)
		r := done.result
		elapsed := int(time.Since(a.started).Seconds())
		setText(a.controls[idStats], fmt.Sprintf("页面 %d  |  资源 %d  |  失败 %d  |  下载 %.1f KB  |  用时 %02d:%02d", r.Pages, r.Assets, r.Failed, float64(r.Bytes)/1024, elapsed/60, elapsed%60))
		for _, warning := range r.Warnings {
			a.appendLog(warning)
		}
		status := "下载完成"
		if errors.Is(done.err, context.Canceled) {
			status = "已取消 · 已下载文件保留"
		} else if done.err != nil {
			status = "任务未完成"
			a.fail(done.err)
		} else if done.result.Failed > 0 {
			status = "完成但有失败项 · 请查看日志"
		}
		setText(a.controls[idStatus], status)
		if done.result.Directory != "" {
			a.appendLog("输出目录：" + done.result.Directory)
		}
	default:
	}
}

func (a *application) command(id int) {
	if a.busy && id != idCancel && id != idHelp {
		return
	}
	switch id {
	case idStart:
		a.start()
	case idCancel:
		if a.cancel != nil {
			a.cancel()
			enabled(a.controls[idCancel], false)
			setText(a.controls[idStatus], "正在取消并整理已下载资源…")
		}
	case idFolder:
		if p := chooseFolder(a.hwnd); p != "" {
			setText(a.controls[idOutput], p)
		}
	case idImportURL:
		p := chooseFile(a.hwnd, false, "从文本文件导入网址", "txt", "")
		if p == "" {
			return
		}
		info, e := os.Stat(p)
		if e != nil {
			a.fail(e)
			return
		}
		if info.Size() > 65536 {
			a.fail(errors.New("网址文件不能超过 64 KB"))
			return
		}
		b, e := os.ReadFile(p)
		if e != nil {
			a.fail(e)
			return
		}
		s := strings.TrimPrefix(string(b), "\ufeff")
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				setText(a.controls[idURL], line)
				break
			}
		}
	case idSaveConfig:
		c, e := a.config()
		if e != nil {
			a.fail(e)
			return
		}
		p := chooseFile(a.hwnd, true, "保存配置（不含 Cookie）", "json", "siteclone-config.json")
		if p != "" {
			a.fail(clone.SaveConfig(p, c))
		}
	case idLoadConfig:
		p := chooseFile(a.hwnd, false, "导入任务配置", "json", "")
		if p == "" {
			return
		}
		c, e := clone.LoadConfig(p)
		if e != nil {
			a.fail(e)
			return
		}
		a.applyConfig(c)
		a.check(idAuthorize, false)
		a.appendLog("已导入配置；需重新确认授权。")
	case idPreview:
		if a.result.Entry == "" {
			return
		}
		if messageBox(a.hwnd, "预览安全提示", "克隆页面将执行原站脚本，并可能访问远端接口。\n请仅预览可信网站，不要在克隆页输入真实账号或敏感信息。\n\n是否打开本地预览？", 0x24) != 6 {
			return
		}
		if a.preview == nil {
			p, e := clone.StartPreview(a.result.Directory, a.result.Entry)
			if e != nil {
				a.fail(e)
				return
			}
			a.preview = p
		}
		a.fail(openExternal(a.hwnd, a.preview.URL))
	case idOpenOutput:
		a.fail(openExternal(a.hwnd, a.result.Directory))
	case idZIP:
		p := chooseFile(a.hwnd, true, "导出 ZIP（不能覆盖已有文件）", "zip", filepath.Base(a.result.Directory)+".zip")
		if p == "" {
			return
		}
		a.exporting = true
		a.setBusy(true)
		setText(a.controls[idStatus], "正在导出 ZIP…")
		root := a.result.Directory
		go func() { a.finished <- completion{export: true, destination: p, err: clone.ExportZIP(root, p)} }()
	case idHelp:
		messageBox(a.hwnd, "使用说明", helpText, 0x40)
	}
}

const helpText = "1. 填写 HTTP(S) 下载网址和保存目录。\n2. 根据需要限定域名、路径、深度、页数与大小。\n3. 如资源托管于 CDN，可勾选允许跨域资源。\n4. 确认授权后开始下载，完成后可预览、打开目录或导出 ZIP。\n\n每次任务创建独立目录，不覆盖已有克隆。深度 0 仅抓取起始页面及其资源。资源计数和页面计数各有限额。\n\n支持 HTML 链接、CSS url()/@import、图片、字体、普通脚本、内联样式和普通 srcset。保留能独立运行的原有前端交互。\n\n不执行 JavaScript、不还原服务端、不自动提取浏览器 Cookie、不绕过登录或验证码。JS 动态资源、ES 模块依赖、接口交互、Service Worker 等可能无法离线运行。\n\n表单提交及 HTML 自动跳转被禁用，避免误操作；脚本仍可能联网。配置不保存 Cookie，但页面本身可能含隐私数据，导出前请检查。\n\n仅对有权复制的网站使用，并遵守目标站点访问规则。窗口内容可使用右侧滚动条滚动。"

func windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	if app == nil {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
		return r
	}
	switch msg {
	case 0x111:
		if wparam>>16 == 0 {
			app.command(int(wparam & 0xffff))
		}
		return 0
	case 0x113:
		app.tick()
		return 0
	case 5:
		app.layout()
		return 0
	case 0x24:
		m := (*minMaxInfo)(unsafe.Pointer(lparam))
		m.MinTrack.X = app.px(1000)
		m.MinTrack.Y = app.px(440)
		return 0
	case 0x115:
		switch wparam & 0xffff {
		case 0:
			app.scroll -= 32
		case 1:
			app.scroll += 32
		case 2:
			app.scroll -= app.height - 40
		case 3:
			app.scroll += app.height - 40
		case 4, 5:
			si := scrollInfo{Mask: 0x10}
			si.Size = uint32(unsafe.Sizeof(si))
			user32.NewProc("GetScrollInfo").Call(hwnd, 1, uintptr(unsafe.Pointer(&si)))
			app.scroll = int(si.TrackPos)
		}
		app.layout()
		return 0
	case 0x20A:
		app.scroll -= int(int16(wparam>>16)) / 120 * 64
		app.layout()
		return 0
	case 0x138:
		gdi32.NewProc("SetBkMode").Call(wparam, 1)
		gdi32.NewProc("SetTextColor").Call(wparam, 0x004E3E26)
		return app.brush
	case 0x10:
		if app.busy {
			messageBox(hwnd, "任务进行中", "请先取消下载并等待任务停止；ZIP 导出需等待完成后再关闭窗口。", 0x40)
			return 0
		}
		if app.preview != nil {
			app.preview.Close()
		}
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 2:
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

func runGUI() error {
	user32.NewProc("SetProcessDPIAware").Call()
	ole32.NewProc("CoInitializeEx").Call(0, 2)
	defer ole32.NewProc("CoUninitialize").Call()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	app = &application{instance: instance, controls: map[int]uintptr{}, events: make(chan clone.Event, 256), finished: make(chan completion, 1), scale: 1}
	dc, _, _ := user32.NewProc("GetDC").Call(0)
	dpi, _, _ := gdi32.NewProc("GetDeviceCaps").Call(dc, 90)
	user32.NewProc("ReleaseDC").Call(0, dc)
	if dpi > 0 {
		app.scale = float64(dpi) / 96
	}
	font := func(height, weight int) uintptr {
		v, _, _ := gdi32.NewProc("CreateFontW").Call(signed(-app.px(height)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(wide("Microsoft YaHei UI"))))
		return v
	}
	app.font = font(15, 400)
	app.titleFont = font(27, 600)
	app.brush, _, _ = gdi32.NewProc("CreateSolidBrush").Call(0x00F5F6F4)
	defer gdi32.NewProc("DeleteObject").Call(app.font)
	defer gdi32.NewProc("DeleteObject").Call(app.titleFont)
	defer gdi32.NewProc("DeleteObject").Call(app.brush)
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ := user32.NewProc("LoadIconW").Call(0, 32512)
	wc := windowClass{Proc: syscall.NewCallback(windowProc), Instance: instance, Cursor: cursor, Icon: icon, Background: app.brush, ClassName: wide("SiteCloneNativeWindow")}
	wc.Size = uint32(unsafe.Sizeof(wc))
	atom, _, e := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return e
	}
	var work rect
	user32.NewProc("SystemParametersInfoW").Call(0x30, 0, uintptr(unsafe.Pointer(&work)), 0)
	w, h := app.px(1000), app.px(940)
	if h > work.Bottom-work.Top-30 {
		h = work.Bottom - work.Top - 30
	}
	x := work.Left + (work.Right-work.Left-w)/2
	y := work.Top + (work.Bottom-work.Top-h)/2
	hwnd, _, e := createWindow.Call(0x10000, uintptr(unsafe.Pointer(wc.ClassName)), uintptr(unsafe.Pointer(wide("网站克隆工具 | Go 原生桌面版"))), 0x00CF0000|0x00200000, signed(x), signed(y), signed(w), signed(h), 0, 0, instance, 0)
	if hwnd == 0 {
		return e
	}
	app.hwnd = hwnd
	app.createControls()
	app.layout()
	user32.NewProc("SetTimer").Call(hwnd, 1, 150, 0)
	showWindow.Call(hwnd, 1)
	user32.NewProc("UpdateWindow").Call(hwnd)
	var m message
	for {
		r, _, e := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 {
			return e
		}
		if r == 0 {
			break
		}
		if handled, _, _ := user32.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&m))); handled != 0 {
			continue
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}
