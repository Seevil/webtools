package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"siteclone/internal/clone"
)

//go:embed assets/mark.svg
var markSVG []byte

//go:embed assets/app.png
var appIconPNG []byte

var appMark = ui.MustParseSVG(markSVG)

type page int

const (
	pageTask page = iota
	pagePaths
	pageRequest
	pageProgress
	pageHistory
	pageHelp
)

var pageNames = [...]string{"新建任务", "文件路径", "请求设置", "下载进度", "任务历史", "使用说明"}
var pageHints = [...]string{
	"填写目标网址、输出位置与页面抓取范围",
	"按类型整理静态资源，页面引用自动改写为这些目录",
	"请求身份、User-Agent 与下载并发、重试和限制",
	"实时进度、下载速度、失败重试与运行日志",
	"查看历史任务，预览、定位目录或续传未完成项",
	"功能说明与安全提示",
}

const (
	encodingKeep = "保持原编码"
	encodingUTF8 = "转换为 UTF-8"
	uaCustom     = "自定义"
)

var encodingOptions = []string{encodingKeep, encodingUTF8}

type uaPreset struct{ name, value string }

var uaPresets = []uaPreset{
	{"电脑 Chrome", clone.DefaultUserAgent},
	{"电脑 Edge", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36 Edg/150.0.0.0"},
	{"电脑 Firefox", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:150.0) Gecko/20100101 Firefox/150.0"},
	{"Mac Safari", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15"},
	{"安卓 Chrome", "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Mobile Safari/537.36"},
	{"iPhone Safari", "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1"},
}

func uaOptions() []string {
	out := make([]string, 0, len(uaPresets)+1)
	for _, p := range uaPresets {
		out = append(out, p.name)
	}
	return append(out, uaCustom)
}

func presetFor(ua string) string {
	for _, p := range uaPresets {
		if strings.TrimSpace(ua) == p.value {
			return p.name
		}
	}
	return uaCustom
}

var refererShortcuts = []struct{ name, value string }{
	{"Google", "https://www.google.com/"},
	{"Baidu", "https://www.baidu.com/"},
	{"Bing", "https://www.bing.com/"},
	{"清空", ""},
}

type form struct {
	URL, Domains, Include, Exclude, Output                string
	Depth, Pages, Assets, Timeout, Delay, FileMB, TotalMB string
	Concurrency, Retries                                  string
	Cookie, UserAgent, UAPreset, Referer, Encoding        string
	FilePaths                                             clone.FilePaths
	ExternalAssets, FilterLanguages                       bool
}

type application struct {
	window                                   *mygo.Window
	form                                     form
	page                                     page
	busy, exporting, cancelling, choosingZIP bool
	cancel                                   context.CancelFunc
	result                                   clone.Result
	preview                                  *clone.Preview
	previewDir                               string
	previewTarget                            [2]string
	last                                     clone.Event
	started, finished, lastEventAt, speedAt  time.Time
	speedBytes                               int64
	speed                                    float64
	logs                                     []string
	logScroll                                ui.ScrollState
	status, errorText, target                string
	mobileMenu                               bool
	confirmPreview                           bool
	pendingDialogs                           int
	domainAuto                               bool
	history                                  []clone.HistoryEntry
	historyFile, currentID                   string
	pageScroll                               [6]ui.ScrollState
	pauseLog                                 bool
	logQuery, logFilter                      string
	logPage, logVersion                      int
	logCacheVersion, logCacheCount           int
	logCacheQuery, logCacheFilter            string
	logCache                                 []string
	historyQuery, historyFilter              string
	historyPageIndex                         int
	browserChoice, browserStatus             string
	browserSession                           *browserImport
	browserBusy                              bool
}

func defaultForm() form {
	c := clone.DefaultConfig()
	return form{
		Output: c.Output, Depth: strconv.Itoa(c.MaxDepth), Pages: strconv.Itoa(c.MaxPages),
		Assets: strconv.Itoa(c.MaxAssets), Timeout: strconv.Itoa(c.TimeoutSeconds),
		Delay: strconv.Itoa(c.DelayMillis), FileMB: strconv.Itoa(c.MaxFileMB),
		TotalMB: strconv.Itoa(c.MaxTotalMB), Concurrency: strconv.Itoa(c.Concurrency),
		Retries: strconv.Itoa(c.Retries), UserAgent: c.UserAgent, UAPreset: presetFor(c.UserAgent),
		Encoding: encodingKeep, FilePaths: c.FilePaths,
	}
}

func (a *application) config() (clone.Config, error) {
	c := clone.DefaultConfig()
	f := a.form
	c.URL, c.Domains, c.Include, c.Exclude = strings.TrimSpace(f.URL), strings.TrimSpace(f.Domains), strings.TrimSpace(f.Include), strings.TrimSpace(f.Exclude)
	c.Output, c.UserAgent, c.Referer, c.Cookie = strings.TrimSpace(f.Output), strings.TrimSpace(f.UserAgent), strings.TrimSpace(f.Referer), strings.TrimSpace(f.Cookie)
	if c.Domains == "" && a.domainAuto {
		c.Domains = clone.RootDomainWildcard(c.URL)
	}
	c.ExternalAssets = f.ExternalAssets
	c.FilterLanguages = f.FilterLanguages
	c.FilePaths = f.FilePaths
	c.Encoding = ""
	if f.Encoding == encodingUTF8 {
		c.Encoding = "utf-8"
	}
	for _, entry := range []struct {
		name, value string
		target      *int
	}{
		{"最大深度", f.Depth, &c.MaxDepth}, {"最大页数", f.Pages, &c.MaxPages},
		{"资源上限", f.Assets, &c.MaxAssets}, {"超时", f.Timeout, &c.TimeoutSeconds},
		{"请求间隔", f.Delay, &c.DelayMillis}, {"单文件上限", f.FileMB, &c.MaxFileMB},
		{"下载总量", f.TotalMB, &c.MaxTotalMB}, {"并发数", f.Concurrency, &c.Concurrency},
		{"失败重试", f.Retries, &c.Retries},
	} {
		n, err := strconv.Atoi(strings.TrimSpace(entry.value))
		if err != nil {
			return c, fmt.Errorf("%s必须是整数", entry.name)
		}
		*entry.target = n
	}
	_, err := c.Validate()
	return c, err
}

func (a *application) applyConfig(c clone.Config) {
	paths, err := c.EffectiveFilePaths()
	if err != nil {
		a.failure(err)
		return
	}
	encoding := encodingKeep
	if strings.EqualFold(c.Encoding, "utf-8") {
		encoding = encodingUTF8
	}
	a.form = form{
		URL: c.URL, Domains: c.Domains, Include: c.Include, Exclude: c.Exclude,
		Output: c.Output, Depth: strconv.Itoa(c.MaxDepth), Pages: strconv.Itoa(c.MaxPages),
		Assets: strconv.Itoa(c.MaxAssets), Timeout: strconv.Itoa(c.TimeoutSeconds),
		Delay: strconv.Itoa(c.DelayMillis), FileMB: strconv.Itoa(c.MaxFileMB),
		TotalMB: strconv.Itoa(c.MaxTotalMB), Concurrency: strconv.Itoa(c.Concurrency),
		Retries: strconv.Itoa(c.Retries), UserAgent: c.UserAgent, UAPreset: presetFor(c.UserAgent),
		Referer: c.Referer, Encoding: encoding, ExternalAssets: c.ExternalAssets, FilterLanguages: c.FilterLanguages, FilePaths: paths,
	}
	a.domainAuto = c.Domains == "" || c.Domains == clone.RootDomainWildcard(c.URL)
	a.syncDomain()
}

func (a *application) syncDomain() {
	if a.domainAuto {
		a.form.Domains = clone.RootDomainWildcard(a.form.URL)
	}
}

func outputFolderName(raw string) string {
	if domain := clone.SiteDomain(raw); domain != "" {
		return domain
	}
	return "输入有效网址后显示"
}

func (a *application) onURLChanged() { a.syncDomain() }

func (a *application) onDomainChanged() {
	a.domainAuto = a.form.Domains == "" || strings.EqualFold(strings.TrimSpace(a.form.Domains), clone.RootDomainWildcard(a.form.URL))
	if a.domainAuto {
		a.syncDomain()
	}
}

func (a *application) enableDomainAuto() {
	a.domainAuto = true
	a.syncDomain()
}

func (a *application) onUAPresetChanged() {
	for _, p := range uaPresets {
		if p.name == a.form.UAPreset {
			a.form.UserAgent = p.value
			return
		}
	}
}

func (a *application) onUAChanged() { a.form.UAPreset = presetFor(a.form.UserAgent) }

func (a *application) appendLog(message string) {
	if message == "" {
		return
	}
	a.logVersion++
	a.logs = append(a.logs, time.Now().Format("15:04:05")+"  "+message)
	if len(a.logs) > 400 {
		a.logs = a.logs[len(a.logs)-400:]
	}
}

func (a *application) failure(err error) {
	if err == nil {
		return
	}
	a.errorText = err.Error()
	a.appendLog("操作失败：" + err.Error())
}

func (a *application) onEvent(e clone.Event) {
	now := time.Now()
	if a.speedAt.IsZero() {
		a.speedAt, a.speedBytes = now, e.Bytes
	} else if dt := now.Sub(a.speedAt).Seconds(); dt >= 0.8 {
		a.speed = float64(e.Bytes-a.speedBytes) / dt
		a.speedAt, a.speedBytes = now, e.Bytes
	}
	a.last, a.lastEventAt = e, now
	a.appendLog(e.Message)
}

func (a *application) findHistory(id string) (clone.HistoryEntry, bool) {
	for _, h := range a.history {
		if h.ID == id {
			return h, true
		}
	}
	return clone.HistoryEntry{}, false
}

func (a *application) recordHistory(h clone.HistoryEntry) {
	a.history = clone.UpsertHistory(a.history, h)
	if a.historyFile == "" {
		return
	}
	if err := clone.SaveHistory(a.historyFile, a.history); err != nil {
		a.appendLog("历史记录保存失败：" + err.Error())
	}
}

func (a *application) deleteHistory(id string) {
	if a.busy && id == a.currentID {
		return
	}
	out := a.history[:0:0]
	for _, h := range a.history {
		if h.ID != id {
			out = append(out, h)
		}
	}
	a.history = out
	if a.historyFile != "" {
		a.failure(clone.SaveHistory(a.historyFile, a.history))
	}
}

type runner func(context.Context, func(clone.Event)) (clone.Result, error)

func (a *application) runTask(target, id string, run runner) {
	if a.preview != nil {
		_ = a.preview.Close()
		a.preview, a.previewDir = nil, ""
	}
	a.result, a.last = clone.Result{}, clone.Event{}
	a.logs, a.errorText, a.target = nil, "", target
	a.logVersion++
	a.pauseLog, a.logPage = false, 0
	a.pageScroll[pageProgress].Y = 0
	a.logScroll.Y = 0
	a.started, a.finished, a.lastEventAt, a.speedAt = time.Now(), time.Time{}, time.Time{}, time.Time{}
	a.speed, a.speedBytes = 0, 0
	a.busy, a.exporting, a.cancelling = true, false, false
	a.status = "正在下载…"
	a.page = pageProgress
	entry := clone.HistoryEntry{ID: id, URL: target, Started: a.started, Status: "进行中"}
	if old, ok := a.findHistory(id); ok {
		entry = old
		entry.Status, entry.Resumable = "续传中", false
	}
	if entry.ID == "" {
		entry.ID = a.started.Format("20060102-150405.000")
	}
	a.currentID = entry.ID
	a.recordHistory(entry)
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go func() {
		defer cancel()
		var pending []clone.Event
		lastFlush := time.Time{}
		flush := func() {
			if len(pending) == 0 {
				return
			}
			batch := pending
			pending = nil
			lastFlush = time.Now()
			a.window.Update(func() {
				for _, e := range batch[:len(batch)-1] {
					a.appendLog(e.Message)
				}
				a.onEvent(batch[len(batch)-1])
			})
		}
		result, runErr := run(ctx, func(e clone.Event) {
			pending = append(pending, e)
			if len(pending) >= 64 || time.Since(lastFlush) >= 100*time.Millisecond {
				flush()
			}
		})
		flush()
		a.window.Update(func() { a.finish(entry, result, runErr) })
	}()
}

func (a *application) finish(entry clone.HistoryEntry, result clone.Result, runErr error) {
	a.busy, a.cancel, a.result, a.finished = false, nil, result, time.Now()
	a.speed = 0
	for _, warning := range result.Warnings {
		a.appendLog(warning)
	}
	if result.Directory != "" {
		a.appendLog("输出目录：" + result.Directory)
	}
	switch {
	case errors.Is(runErr, context.Canceled):
		a.status, entry.Status = "任务已取消 · 已下载文件保留，可续传", "已取消"
	case runErr != nil:
		a.status, entry.Status = "任务未完成", "未完成"
		a.failure(runErr)
	case result.Failed > 0:
		a.status, entry.Status = fmt.Sprintf("下载完成，%d 项失败 · 可点击“续传失败项”重试", result.Failed), "部分失败"
	case result.Resumable:
		a.status, entry.Status = "任务未完成 · 已保留文件，请查看日志中的限制原因", "未完成"
	default:
		a.status, entry.Status = "下载完成 · 可以预览或导出 ZIP", "已完成"
	}
	entry.Directory, entry.Entry, entry.Finished = result.Directory, result.Entry, a.finished
	entry.Pages, entry.Assets, entry.Failed, entry.Bytes = result.Pages, result.Assets, result.Failed, result.Bytes
	entry.Resumable = result.Resumable
	a.recordHistory(entry)
}

func (a *application) start() {
	if a.busy || a.choosingZIP || a.pendingDialogs > 0 {
		return
	}
	cfg, err := a.config()
	if err != nil {
		a.failure(err)
		return
	}
	a.runTask(cfg.URL, "", func(ctx context.Context, emit func(clone.Event)) (clone.Result, error) {
		return clone.Run(ctx, cfg, emit)
	})
}

func (a *application) resume(h clone.HistoryEntry) {
	if a.busy || a.choosingZIP || a.pendingDialogs > 0 || h.Directory == "" {
		return
	}
	cookie := strings.TrimSpace(a.form.Cookie)
	dir := h.Directory
	a.runTask(h.URL, h.ID, func(ctx context.Context, emit func(clone.Event)) (clone.Result, error) {
		return clone.Resume(ctx, dir, cookie, emit)
	})
}

func (a *application) cancelRun() {
	if a.cancel == nil {
		return
	}
	a.cancel()
	a.cancelling = true
	a.status = "正在取消并保存续传状态…"
}

func (a *application) chooseDirectory() {
	if a.busy || a.pendingDialogs > 0 {
		return
	}
	a.pendingDialogs++
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择保存目录", Directory: true})
		a.window.Update(func() {
			a.pendingDialogs--
			if err != nil {
				a.failure(err)
			} else if len(paths) > 0 && !a.busy {
				a.form.Output = paths[0]
			}
		})
	}()
}

func (a *application) importURL() {
	if a.busy || a.pendingDialogs > 0 {
		return
	}
	a.pendingDialogs++
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "导入网址文本", Filters: []mygo.FileFilter{{Name: "文本文件", Extensions: []string{"txt"}}}})
		if err != nil || len(paths) == 0 {
			a.window.Update(func() { a.pendingDialogs--; a.failure(err) })
			return
		}
		info, err := os.Stat(paths[0])
		if err == nil && (!info.Mode().IsRegular() || info.Size() > 65536) {
			err = errors.New("网址文件必须是小于 64 KB 的普通文件")
		}
		var data []byte
		if err == nil {
			data, err = os.ReadFile(paths[0])
		}
		a.window.Update(func() {
			a.pendingDialogs--
			if err != nil {
				a.failure(err)
				return
			}
			if a.busy {
				return
			}
			for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					a.form.URL = line
					a.syncDomain()
					break
				}
			}
		})
	}()
}

func (a *application) saveConfig() {
	if a.busy || a.pendingDialogs > 0 {
		return
	}
	cfg, err := a.config()
	if err != nil {
		a.failure(err)
		return
	}
	a.pendingDialogs++
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.window, Title: "保存任务配置（不含 Cookie）", DefaultPath: "siteclone-config.json", Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if err == nil && path != "" {
			err = clone.SaveConfig(path, cfg)
		}
		a.window.Update(func() {
			a.pendingDialogs--
			a.failure(err)
			if err == nil && path != "" {
				a.appendLog("配置已保存（不含 Cookie）")
			}
		})
	}()
}

func (a *application) loadConfig() {
	if a.busy || a.pendingDialogs > 0 {
		return
	}
	a.pendingDialogs++
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "导入任务配置", Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if err != nil || len(paths) == 0 {
			a.window.Update(func() { a.pendingDialogs--; a.failure(err) })
			return
		}
		cfg, err := clone.LoadConfig(paths[0])
		a.window.Update(func() {
			a.pendingDialogs--
			if err != nil {
				a.failure(err)
				return
			}
			if a.busy {
				return
			}
			a.applyConfig(cfg)
			a.appendLog("已导入配置（Cookie 不会载入）")
		})
	}()
}

func (a *application) requestPreview(dir, entry string) {
	if dir == "" || entry == "" {
		return
	}
	a.previewTarget = [2]string{dir, entry}
	a.confirmPreview = true
}

func (a *application) openPreview() {
	dir, entry := a.previewTarget[0], a.previewTarget[1]
	if dir == "" || entry == "" {
		return
	}
	if a.preview != nil && a.previewDir != dir {
		_ = a.preview.Close()
		a.preview = nil
	}
	if a.preview == nil {
		preview, err := clone.StartPreview(dir, entry)
		if err != nil {
			a.failure(err)
			return
		}
		a.preview, a.previewDir = preview, dir
	}
	a.failure(mygo.Shell.OpenExternal(a.preview.URL))
}

func (a *application) openFolder(dir string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		a.failure(errors.New("目录不存在或已被移动：" + dir))
		return
	}
	mygo.Shell.ShowItemInFolder(dir)
}

func (a *application) exportZIP() {
	if a.result.Entry == "" || a.busy || a.choosingZIP || a.pendingDialogs > 0 {
		return
	}
	a.choosingZIP = true
	root := a.result.Directory
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.window, Title: "导出 ZIP（不覆盖已有文件）", DefaultPath: filepath.Base(root) + ".zip", Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}}})
		if err != nil || path == "" {
			a.window.Update(func() {
				a.choosingZIP = false
				a.failure(err)
			})
			return
		}
		a.window.Update(func() { a.busy, a.exporting, a.status = true, true, "正在导出 ZIP…" })
		err = clone.ExportZIP(root, path)
		a.window.Update(func() {
			a.choosingZIP, a.busy, a.exporting = false, false, false
			if err != nil {
				a.status = "ZIP 导出失败"
				a.failure(err)
			} else {
				a.status = "ZIP 导出完成"
				a.appendLog("ZIP 已保存：" + path)
			}
		})
	}()
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

// stats merges the live event with the final result once a task has finished.
func (a *application) stats() (clone.Event, time.Duration, float64) {
	e := a.last
	if !a.busy && a.result.Directory != "" {
		e.Pages, e.Assets, e.Failed, e.Bytes = a.result.Pages, a.result.Assets, a.result.Failed, a.result.Bytes
		e.Retries, e.Pending, e.Active = a.result.Retries, 0, 0
	}
	elapsed := time.Duration(0)
	switch {
	case a.started.IsZero():
	case a.finished.IsZero():
		elapsed = time.Since(a.started)
	default:
		elapsed = a.finished.Sub(a.started)
	}
	speed := a.speed
	if !a.busy || time.Since(a.lastEventAt) > 3*time.Second {
		speed = 0
	}
	return e, elapsed, speed
}

const uiFontFamily = "Microsoft YaHei UI, Microsoft YaHei, Segoe UI, system-ui"

var (
	colorBG      = ui.Hex("#f7f8f7")
	colorSide    = ui.Hex("#fdfefd")
	colorSideEnd = ui.Hex("#fdfefd")
	colorCard    = ui.Hex("#ffffff")
	colorLine    = ui.Hex("#d8dfda")
	colorTitle   = ui.Hex("#1d2924")
	colorSoft    = ui.Hex("#f1f5f2")
	colorStatus  = ui.Hex("#edf2ee")
	colorOnSide  = ui.Hex("#2e4038")
	colorSideDim = ui.Hex("#52645c")
	colorAccent  = ui.Hex("#dcebe2")
	colorInk     = ui.Hex("#45564e")
	colorStrong  = ui.Hex("#2f6650")
)

func (a *application) view(c *ui.Context) {
	t := *ui.LightTheme()
	t.Accent, t.AccentHover, t.AccentPressed = colorStrong, ui.Hex("#285844"), ui.Hex("#214936")
	t.Background, t.Text, t.TextMuted = colorBG, colorTitle, colorInk
	t.Surface, t.SurfaceHover, t.SurfacePressed = colorCard, colorSoft, colorAccent
	t.Border, t.Focus = colorLine, t.Accent
	t.Radius, t.Spacing = 7, 4
	t.Font, t.FontSize = uiFontFamily, 13
	c.SetTheme(&t)
	width, _ := c.Size()
	compact := width < 800
	ui.Column(c).Fill().Background(colorBG).Children(func() {
		if compact {
			ui.Row(c).FillWidth().Padding(10, 14).Gap(10).Background(colorSide).Border(1, colorLine).Children(func() {
				ui.Button(c, "菜单").OnClick(func() { a.mobileMenu = !a.mobileMenu })
				ui.Image(c, appMark).Width(28).Height(28)
				ui.Text(c, "网站克隆工具").Grow(1).Bold().TextColor(colorTitle)
				ui.Text(c, pageNames[a.page]).FontSize(12).TextColor(colorSideDim)
			})
			if a.mobileMenu {
				a.compactNavigation(c)
			}
		}
		ui.Row(c).FillWidth().Grow(1).Children(func() {
			if !compact {
				a.sidebar(c)
			}
			padding := float32(26)
			if compact {
				padding = 16
			}
			ui.Column(c).Grow(1).FillHeight().Children(func() {
				ui.Scroll(c).Key(fmt.Sprintf("page-%d", a.page)).TrackScroll(&a.pageScroll[a.page]).Grow(1).FillWidth().Padding(20, padding).Gap(18).Background(colorBG).Children(func() {
					a.pageHeader(c)
					if a.errorText != "" {
						ui.Row(c).FillWidth().Padding(10, 14).Gap(10).Radius(8).Background(ui.Hex("#fdecea")).Children(func() {
							ui.Text(c, a.errorText).Grow(1).TextColor(c.Theme().Danger).Selectable()
							ui.Button(c, "关闭").OnClick(func() { a.errorText = "" })
						})
					}
					switch a.page {
					case pageTask:
						a.taskPage(c)
					case pagePaths:
						a.pathsPage(c)
					case pageRequest:
						a.requestPage(c)
					case pageProgress:
						a.progressPage(c)
					case pageHistory:
						a.historyPage(c)
					default:
						a.helpPage(c)
					}
				})
				a.actionBar(c)
			})
		})
		a.statusBar(c)
	})
	if a.busy && !a.exporting {
		c.After(time.Second)
	}
	if ui.AlertDialog(c, &a.confirmPreview, "预览安全提示", "克隆页面可能执行原站脚本并访问远端接口。仅预览可信站点，不要输入真实账号或敏感信息。", "取消", "打开预览") == 1 {
		a.openPreview()
	}
}

func (a *application) compactNavigation(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(10, 12).Gap(8).Wrap().Background(colorSideEnd).Children(func() {
		for i, name := range pageNames {
			p := page(i)
			ui.Button(c, name).OnClick(func() { a.page, a.mobileMenu = p, false })
		}
	})
}

func (a *application) sidebar(c *ui.Context) {
	ui.Column(c).Width(218).FillHeight().Padding(24, 15).Gap(8).Background(colorSide).Border(1, colorLine).Children(func() {
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			ui.Image(c, appMark).Width(38).Height(38)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "网站克隆工具").FontSize(17).Bold().TextColor(colorTitle)
				ui.Text(c, "SITECLONE  /  工作台").FontSize(10).TextColor(colorSideDim)
			})
		})
		ui.Column(c).Height(19)
		ui.Text(c, "下载工作台").FontSize(11).TextColor(colorSideDim).Padding(0, 10)
		for i, name := range pageNames {
			p := page(i)
			if p == pageHistory {
				ui.Column(c).Height(10)
				ui.Text(c, "管理与帮助").FontSize(11).TextColor(colorSideDim).Padding(0, 10)
			}
			label := name
			if p == pageProgress && a.busy {
				label += "  · 运行中"
			}
			active := a.page == p
			background, ink := colorSoft, colorOnSide
			if active {
				background, ink = colorAccent, colorTitle
			}
			ui.ButtonBase(c).FillWidth().Height(44).Padding(0, 13).Radius(10).Background(background).
				OnClick(func() { a.page = p }).Label(name).Children(func() {
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					if active {
						ui.Box(c).Width(3).Height(18).Radius(2).Background(colorStrong)
					} else {
						ui.Box(c).Width(3).Height(18)
					}
					ui.Text(c, label).Grow(1).FontSize(13).TextColor(ink)
					if active {
						ui.Text(c, "›").Bold().TextColor(colorInk)
					}
				})
			})
		}
		ui.Spacer(c)
		state := "准备就绪"
		if a.busy {
			state = "任务运行中"
		}
		ui.Column(c).FillWidth().Padding(12).Gap(4).Radius(10).Background(colorSoft).Border(1, colorLine).Children(func() {
			ui.Text(c, state).Bold().FontSize(12).TextColor(colorTitle)
			ui.Text(c, "本地保存 · 按需续传").FontSize(11).TextColor(colorSideDim)
		})
	})
}

func (a *application) pageHeader(c *ui.Context) {
	width, _ := c.Size()
	compact := width < 800
	controls := func() {
		if a.page == pageTask || a.page == pageRequest || a.page == pagePaths {
			ui.Button(c, "导入配置").Disabled(a.busy || a.pendingDialogs > 0).OnClick(a.loadConfig)
			ui.Button(c, "保存配置").Disabled(a.busy || a.pendingDialogs > 0).OnClick(a.saveConfig)
		}
	}
	title := func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, "网站克隆  /  "+pageNames[a.page]).FontSize(11).Bold().TextColor(colorInk)
			ui.Text(c, pageNames[a.page]).FontSize(28).Bold().TextColor(colorTitle)
			ui.Text(c, pageHints[a.page]).FontSize(12).TextColor(colorInk)
		})
	}
	ui.Column(c).FillWidth().Padding(17, 21).Gap(12).Radius(9).
		Background(colorCard).Border(1, colorLine).Children(func() {
		if compact {
			title()
			ui.Row(c).FillWidth().Gap(8).Wrap().Children(controls)
			return
		}
		ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
			title()
			controls()
		})
	})
}

func (a *application) card(c *ui.Context, title string, children func()) {
	ui.Column(c).FillWidth().Padding(18, 20).Gap(14).Background(colorCard).Radius(9).
		Border(1, colorLine).Children(func() {
		ui.Text(c, title).FontSize(16).Bold().TextColor(colorTitle)
		ui.Divider(c)
		children()
	})
}

func (a *application) inputRow(c *ui.Context, label string, value *string, hint, action string, onChange, onAction func()) {
	width, _ := c.Size()
	input := func() {
		field := ui.TextInput(c, value).Grow(1).Placeholder(hint).Label(label).Disabled(a.busy)
		if onChange != nil {
			field.OnChange(onChange)
		}
	}
	button := func() {
		if action != "" {
			ui.Button(c, action).Disabled(a.busy || a.pendingDialogs > 0).OnClick(onAction)
		}
	}
	if width < 800 {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(colorInk)
			ui.Row(c).FillWidth().Children(input)
			button()
		})
		return
	}
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, label).Width(94)
		input()
		button()
	})
}

func (a *application) field(c *ui.Context, label string, value *string, hint string) {
	a.inputRow(c, label, value, hint, "", nil, nil)
}

func (a *application) taskInputRow(c *ui.Context, label string, value *string, hint, action string, onChange, onAction func()) {
	width, _ := c.Size()
	if width < 800 {
		a.inputRow(c, label, value, hint, action, onChange, onAction)
		return
	}
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, label).Width(94)
		field := ui.TextInput(c, value).Grow(1).Placeholder(hint).Label(label).Disabled(a.busy)
		if onChange != nil {
			field.OnChange(onChange)
		}
		if action == "" {
			ui.Box(c).Width(112).Height(1)
		} else {
			ui.Button(c, action).Width(112).Disabled(a.busy || a.pendingDialogs > 0).OnClick(onAction)
		}
	})
}

func (a *application) smallField(c *ui.Context, label string, value *string) {
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, label).Width(86)
		ui.TextInput(c, value).Width(72).Label(label).Disabled(a.busy)
	})
}

func (a *application) limitField(c *ui.Context, label string, value *string) {
	width, _ := c.Size()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(colorInk)
		input := ui.TextInput(c, value).Label(label).Disabled(a.busy)
		if width < 520 {
			input.FillWidth()
		} else {
			input.Width(200)
		}
	})
}

func (a *application) encodingField(c *ui.Context) {
	width, _ := c.Size()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, "编码转换").FontSize(12).TextColor(colorInk)
		selectField := ui.Select(c, &a.form.Encoding, encodingOptions).Label("编码转换").Disabled(a.busy)
		if width < 520 {
			selectField.FillWidth()
		} else {
			selectField.Width(200)
		}
	})
}

func (a *application) taskPage(c *ui.Context) {
	a.card(c, "目标、输出与抓取范围", func() {
		ui.Text(c, "填写目标网址和保存位置；域名及路径规则用于控制页面发现范围。").FontSize(12).TextColor(colorInk)
		a.taskInputRow(c, "下载网址", &a.form.URL, "https://example.org/", "导入网址", a.onURLChanged, a.importURL)
		domainAction := "自动识别"
		if a.domainAuto {
			domainAction = "已自动识别"
		}
		a.taskInputRow(c, "限定域名", &a.form.Domains, "可输入多个域名，用分号分隔", domainAction, a.onDomainChanged, a.enableDomainAuto)
		a.taskInputRow(c, "限定路径", &a.form.Include, "例如 /docs；用分号分隔", "", nil, nil)
		a.taskInputRow(c, "排除路径", &a.form.Exclude, "例如 /private；用分号分隔", "", nil, nil)
		a.taskInputRow(c, "保存目录", &a.form.Output, "选择本地保存位置", "选择文件夹", nil, a.chooseDirectory)
		ui.Checkbox(c, &a.form.FilterLanguages, "过滤其他语言目录（自动识别 /en/、/fr/ 等；保留起始语言）").Disabled(a.busy)
		width, _ := c.Size()
		columns := 1
		if width >= 980 {
			columns = 4
		} else if width >= 520 {
			columns = 2
		}
		ui.Grid(c).FillWidth().Columns(columns).Gap(16).Children(func() {
			a.limitField(c, "最大深度", &a.form.Depth)
			a.limitField(c, "最大页数", &a.form.Pages)
			a.limitField(c, "下载超时 /秒", &a.form.Timeout)
			a.encodingField(c)
		})
		ui.Row(c).FillWidth().Padding(10, 14).Radius(7).Background(colorSoft).Children(func() {
			ui.Textf(c, "任务文件夹  /  %s", outputFolderName(a.form.URL)).FontSize(12).Bold().TextColor(colorInk).SingleLine()
		})
		ui.Text(c, "域名默认提取根域通配，支持手动输入多个域名。语言过滤仅识别首级 /en/、/fr/ 等目录；起始网址带语言目录时保留该语言，普通页面不受影响。静态资源仍按下载范围获取。并发与重试在“请求设置”中调整。").FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (a *application) downloadSettings(c *ui.Context) {
	a.card(c, "下载策略", func() {
		ui.Text(c, "控制下载并发、失败重试和资源限制；配置会随任务配置一起保存。").FontSize(12).TextColor(colorInk)
		width, _ := c.Size()
		columns := 2
		if width < 520 {
			columns = 1
		}
		ui.Grid(c).FillWidth().Columns(columns).Gap(14).Children(func() {
			a.limitField(c, "并发数", &a.form.Concurrency)
			a.limitField(c, "失败重试", &a.form.Retries)
		})
		ui.Checkbox(c, &a.form.ExternalAssets, "允许下载跨域资源（如 CDN、字体和图片）").Disabled(a.busy)
		ui.Divider(c)
		ui.Text(c, "下载限制").Bold().TextColor(colorTitle)
		ui.Grid(c).FillWidth().Columns(columns).Gap(14).Children(func() {
			a.limitField(c, "资源上限", &a.form.Assets)
			a.limitField(c, "间隔 / 毫秒", &a.form.Delay)
			a.limitField(c, "单文件 / MB", &a.form.FileMB)
			a.limitField(c, "总量 / MB", &a.form.TotalMB)
		})
		ui.Text(c, "并发 1–32；网络错误、超时和 HTTP 408/429/5xx 会按设置重试。").FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (a *application) pathsPage(c *ui.Context) {
	a.card(c, "文件结构路径", func() {
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, "快速选择").Width(94)
			ui.Button(c, "恢复默认").Disabled(a.busy).OnClick(func() { a.form.FilePaths = clone.DefaultFilePaths() })
		})
		a.field(c, "脚本 js", &a.form.FilePaths.JS, "/static/js/")
		a.field(c, "样式 css", &a.form.FilePaths.CSS, "/static/css/")
		a.field(c, "素材 image", &a.form.FilePaths.Image, "/static/image/")
		a.field(c, "图片 picture", &a.form.FilePaths.Picture, "/static/picture/")
		a.field(c, "字体 font", &a.form.FilePaths.Font, "/static/font/")
		a.field(c, "文件 file", &a.form.FilePaths.File, "/static/file/")
		ui.Text(c, "页面保存在输出目录根部；资源按类型放入以上目录并保留原文件名，同名同内容直接复用，内容不同才添加区分后缀。").FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (a *application) requestPage(c *ui.Context) {
	a.card(c, "请求身份", func() {
		width, _ := c.Size()
		if width < 800 {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, "Cookie").FontSize(12).TextColor(colorInk)
				ui.TextInput(c, &a.form.Cookie).FillWidth().Label("Cookie").Password().Disabled(a.busy)
			})
		} else {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Text(c, "Cookie").Width(94)
				ui.TextInput(c, &a.form.Cookie).Grow(1).Label("Cookie").Password().Disabled(a.busy)
			})
		}
		ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
			ui.Text(c, "UA 预设").Width(94)
			ui.Select(c, &a.form.UAPreset, uaOptions()).Width(220).Label("UA 预设").Disabled(a.busy).OnChange(a.onUAPresetChanged)
		})
		a.inputRow(c, "User-Agent", &a.form.UserAgent, "请求标识", "", a.onUAChanged, nil)
		a.field(c, "来源网址", &a.form.Referer, "可选，HTTP(S) 地址")
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			ui.Text(c, "快捷来源").Width(94)
			for _, r := range refererShortcuts {
				value := r.value
				ui.Button(c, r.name).Disabled(a.busy).OnClick(func() { a.form.Referer = value })
			}
		})
		ui.Divider(c)
		ui.Text(c, "从浏览器导入").Bold().TextColor(colorTitle)
		ui.Text(c, "使用独立的 SiteClone 浏览器会话，不读取 Chrome/Edge 日常配置。仅导入实际会发送给下载网址的 Cookie，并同步目标根域页面 User-Agent。").FontSize(12).TextColor(colorInk)
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			if a.browserChoice == "" {
				a.browserChoice = "Google Chrome"
			}
			ui.Select(c, &a.browserChoice, []string{"Google Chrome", "Microsoft Edge"}).Width(180).Label("浏览器").Disabled(a.browserBusy || a.browserSession != nil)
			ui.Button(c, "打开登录浏览器").Disabled(a.browserBusy || a.browserSession != nil || strings.TrimSpace(a.form.URL) == "").OnClick(a.openBrowserImport)
			ui.PrimaryButton(c, "读取 Cookie 和 UA").Disabled(a.browserBusy || a.browserSession == nil).OnClick(a.readBrowserImport)
			ui.Button(c, "关闭会话").Disabled(a.browserBusy || a.browserSession == nil).OnClick(a.closeBrowserImport)
		})
		if a.browserStatus != "" {
			ui.Text(c, a.browserStatus).FontSize(12).TextColor(colorInk)
		}
		ui.Column(c).FillWidth().Padding(12, 14).Radius(7).Background(colorSoft).Children(func() {
			ui.Text(c, "Cookie 只存于内存，不写入配置、历史或续传状态；仅向起始网址同源请求发送。续传需要登录的站点时，请先在此重新填写或导入 Cookie。").FontSize(12).TextColor(colorInk)
		})
	})
	a.downloadSettings(c)
}

func (a *application) statTile(c *ui.Context, label, value string) {
	width, _ := c.Size()
	tileWidth := float32(144)
	if width < 800 {
		tileWidth = 112
	}
	ui.Column(c).Width(tileWidth).Padding(13, 14).Gap(5).Radius(10).Background(colorSoft).Border(1, colorLine).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(colorInk)
		ui.Text(c, value).FontSize(17).Bold().TextColor(colorTitle)
	})
}

func (a *application) progressPage(c *ui.Context) {
	e, elapsed, speed := a.stats()
	a.card(c, "任务状态", func() {
		status := a.status
		if status == "" {
			status = "尚未开始任务"
		}
		ui.Column(c).FillWidth().Padding(15, 17).Gap(6).Radius(10).Background(colorSoft).Border(1, colorLine).Children(func() {
			ui.Text(c, status).FontSize(17).Bold().TextColor(colorTitle)
			if a.target != "" {
				ui.Text(c, a.target).FontSize(12).TextColor(colorInk).MaxLines(2).Selectable()
			}
		})
		fraction := -1.0
		switch {
		case !a.busy && a.result.Entry != "" && !a.result.Resumable && !a.result.Cancelled && a.result.Failed == 0:
			fraction = 1
		case e.Total > 0:
			fraction = math.Min(1, float64(e.Done)/float64(e.Total))
		case !a.busy:
			fraction = 0
		}
		ui.Progress(c, fraction).FillWidth().Label("下载进度")
		if e.Total > 0 {
			ui.Textf(c, "已处理 %d / %d 项 · 待处理 %d", e.Done, e.Total, e.Pending).FontSize(12).TextColor(colorInk)
		} else {
			ui.Text(c, "准备好后，在“新建任务”填写网址并开始下载。").FontSize(12).TextColor(colorInk)
		}
	})
	ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
		a.statTile(c, "页面 / 资源", fmt.Sprintf("%d / %d", e.Pages, e.Assets))
		a.statTile(c, "失败", strconv.Itoa(e.Failed))
		a.statTile(c, "已下载", formatBytes(e.Bytes))
		a.statTile(c, "实时速度", formatBytes(int64(speed))+"/s")
	})
	ui.Textf(c, "活动连接 %d · 重试 %d 次 · 用时 %s", e.Active, e.Retries, formatDuration(elapsed)).FontSize(12).TextColor(colorInk)
	a.card(c, "下载结果", func() {
		if a.result.Directory == "" {
			ui.Text(c, "任务完成后，可在此预览页面、打开目录或导出 ZIP。").FontSize(12).TextColor(colorInk)
		} else {
			ui.Text(c, a.result.Directory).FontSize(12).TextColor(colorInk).MaxLines(2).Selectable()
		}
		ui.Row(c).FillWidth().Gap(9).Wrap().Children(func() {
			ui.Button(c, "本地预览").Disabled(a.busy || a.result.Entry == "").OnClick(func() { a.requestPreview(a.result.Directory, a.result.Entry) })
			ui.Button(c, "定位目录").Disabled(a.busy || a.result.Directory == "").OnClick(func() { a.openFolder(a.result.Directory) })
			ui.Button(c, "导出 ZIP").Disabled(a.busy || a.choosingZIP || a.pendingDialogs > 0 || a.result.Entry == "").OnClick(a.exportZIP)
		})
	})
	a.logPanel(c)
}

func (a *application) historyPage(c *ui.Context) {
	if len(a.history) == 0 {
		a.card(c, "历史任务", func() {
			ui.Text(c, "暂无任务记录").TextColor(c.Theme().TextMuted)
		})
		return
	}
	ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
		ui.TextInput(c, &a.historyQuery).Width(220).Label("搜索历史").Placeholder("网址、目录或状态").OnChange(func() { a.historyPageIndex = 0 })
		ui.Select(c, &a.historyFilter, []string{"全部任务", "未完成", "已完成"}).Width(130).Label("任务筛选").OnChange(func() { a.historyPageIndex = 0 })
	})
	history := a.filteredHistory()
	start, end, pages := pageBounds(len(history), historyPageSize, &a.historyPageIndex)
	ui.Textf(c, "匹配 %d / %d 条 · 删除记录不会删除已下载文件", len(history), len(a.history)).FontSize(12).TextColor(c.Theme().TextMuted)
	if len(history) == 0 {
		ui.Text(c, "没有匹配的任务").TextColor(colorInk)
	}
	for _, h := range history[start:end] {
		running := a.busy && h.ID == a.currentID
		ui.Column(c).Key(h.ID).FillWidth().Padding(17, 19).Gap(10).Background(colorCard).Radius(9).
			Border(1, colorLine).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
				ui.Text(c, h.URL).Grow(1).Bold().TextColor(colorTitle).MaxLines(2).Selectable()
				ui.Text(c, h.Status).FontSize(12).TextColor(colorInk).Padding(6, 10).Background(colorSoft).Radius(7)
			})
			ui.Textf(c, "%s  ·  页面 %d  ·  资源 %d  ·  失败 %d  ·  %s", h.Started.Format("2006-01-02 15:04"), h.Pages, h.Assets, h.Failed, formatBytes(h.Bytes)).FontSize(12).TextColor(c.Theme().TextMuted)
			if h.Directory != "" {
				ui.Text(c, h.Directory).FontSize(12).TextColor(c.Theme().TextMuted).Selectable()
			}
			ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
				ui.Button(c, "预览").Disabled(running || h.Entry == "").OnClick(func() { a.requestPreview(h.Directory, h.Entry) })
				ui.Button(c, "打开目录").Disabled(h.Directory == "").OnClick(func() { a.openFolder(h.Directory) })
				label := "续传"
				if !h.Resumable {
					label = "校验修复"
				}
				ui.Button(c, label).Disabled(a.busy || h.Directory == "" || a.pendingDialogs > 0 || a.choosingZIP).OnClick(func() { a.resume(h) })
				ui.Button(c, "删除记录").Disabled(running).OnClick(func() { a.deleteHistory(h.ID) })
			})
		})
	}
	ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
		ui.Button(c, "上一页").Disabled(a.historyPageIndex == 0).OnClick(func() { a.historyPageIndex--; a.pageScroll[pageHistory].Y = 0 })
		ui.Textf(c, "%d / %d 页", a.historyPageIndex+1, pages).FontSize(12)
		ui.Button(c, "下一页").Disabled(a.historyPageIndex+1 == pages).OnClick(func() { a.historyPageIndex++; a.pageScroll[pageHistory].Y = 0 })
	})
}

func (a *application) helpPage(c *ui.Context) {
	for _, section := range helpSections {
		a.card(c, section[0], func() {
			ui.Text(c, section[1]).Selectable()
		})
	}
}

func (a *application) statusBar(c *ui.Context) {
	e, elapsed, speed := a.stats()
	width, _ := c.Size()
	ui.Row(c).FillWidth().Padding(9, 18).Gap(20).Wrap().Background(colorStatus).Children(func() {
		ui.Textf(c, "下载速度：%s/s", formatBytes(int64(speed))).FontSize(12).Bold().TextColor(colorTitle)
		ui.Textf(c, "下载成功：%d", e.Pages+e.Assets).FontSize(12).TextColor(colorInk)
		ui.Textf(c, "失败：%d", e.Failed).FontSize(12).TextColor(colorInk)
		if width >= 800 {
			ui.Textf(c, "下载合计：%s", formatBytes(e.Bytes)).FontSize(12).TextColor(colorInk)
			ui.Textf(c, "用时：%s", formatDuration(elapsed)).FontSize(12).TextColor(colorInk)
		}
	})
}

var helpSections = [][2]string{
	{"基本流程", "在“新建任务”填写下载网址；默认保存目录位于程序所在目录的 downloads 文件夹，限定域名会自动提取为根域名通配（如 *.example.com）。可勾选过滤 /en/、/fr/ 等其他语言分站：保留起始网址所属语言，普通页面不受影响；静态资源仍按下载范围获取。点击开始下载后自动切换到“下载进度”，可查看实时速度、进度和日志。完成后可本地预览、定位目录或导出 ZIP。"},
	{"文件结构", "输出文件夹以网址域名命名，已存在时追加序号。页面保存在根目录；脚本、样式、图片、字体等按“文件路径”页的设置分类保存并保留原文件名，页面与 CSS 中的引用会改写为本地路径。"},
	{"日志与历史", "日志支持关键词、异常及文件复用筛选，按最新在前分页显示，最多保留 400 条；暂停更新只冻结日志显示，不影响下载。导出日志保存当前保留的记录，不是整次任务的完整日志。历史可按网址、目录或状态搜索，并按完成情况筛选。"},
	{"校验与修复", "历史任务可通过“校验修复”检查已有文件的完整性。缺失或被修改的文件会尝试重新下载，保留原修改文件并更新本地引用；旧版状态可能需要重新下载已本地化的页面或样式。此操作需要原站仍可访问，不是离线修复。"},
	{"并发、重试与续传", "并发数控制同时下载的连接数，请求间隔作用于每个连接。网络错误、超时及 HTTP 408/429/5xx 会按设置自动重试。取消或有失败项时，任务状态保存在输出目录的 .siteclone 文件夹，可在“下载进度”或“任务历史”中续传，已下载文件不会重复下载。"},
	{"编码与请求身份", "编码转换可将 GBK、Big5、Shift_JIS 等声明编码的 HTML/CSS 转为 UTF-8 并更新 charset 声明。“请求设置”提供常用 UA 预设和 Google/Baidu/Bing 来源快捷设置；Cookie 仅保存在内存中。"},
	{"限制与安全", "仅克隆 HTTP GET 可获取的前端内容，不执行 JavaScript，不克隆接口、数据库或登录业务。表单提交和自动跳转已禁用，但页面脚本仍可能联网，仅预览可信站点。请遵守目标网站的使用条款与版权要求。"},
}

func main() {
	a := &application{form: defaultForm(), domainAuto: true, status: "就绪 · 请填写网址后点击开始下载"}
	if file, err := clone.HistoryPath(); err == nil {
		a.historyFile = file
		history, err := clone.LoadHistory(file)
		if err != nil {
			a.appendLog("历史记录读取失败：" + err.Error())
		}
		for i := range history {
			if history[i].Status == "进行中" || history[i].Status == "续传中" {
				history[i].Status = "已中断"
			}
		}
		a.history = history
	}
	mygo.App.SetName("网站克隆工具")
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{
			Title: "网站克隆工具 · 下载工作台", Width: 1180, Height: 820,
			MinWidth: 375, MinHeight: 560, StateKey: "main", Content: ui.View(a.view),
		})
		if err := a.window.SetIcon(appIconPNG); err != nil {
			log.Printf("设置窗口图标失败：%v", err)
		}
		a.window.OnClose(func(e *mygo.CloseEvent) {
			if a.busy || a.choosingZIP || a.pendingDialogs > 0 {
				e.PreventDefault()
				a.failure(errors.New("请先结束文件选择，取消下载或等待 ZIP 导出完成"))
				a.window.Invalidate()
			}
		})
		a.window.OnClosed(func() {
			if a.preview != nil {
				_ = a.preview.Close()
			}
			if a.browserSession != nil {
				a.browserSession.Close()
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
