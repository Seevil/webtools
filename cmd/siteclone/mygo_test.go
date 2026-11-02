package main

import (
	"bytes"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"siteclone/internal/clone"
)

func TestEmbeddedAppIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(appIconPNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 || appMark == nil {
		t.Fatal("应用图标尺寸或嵌入资源错误")
	}
	_, _, _, corner := img.At(0, 0).RGBA()
	_, _, _, ink := img.At(52, 116).RGBA()
	if corner != 0 || ink == 0 {
		t.Fatalf("图标透明度错误：四角 alpha=%d，主体 alpha=%d", corner, ink)
	}
	minX, minY, maxX, maxY := 256, 256, -1, -1
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha <= 16*257 {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if minX > 24 || minY > 24 || maxX < 231 || maxY < 231 {
		t.Fatalf("图标主体过小：范围为 (%d,%d)-(%d,%d)", minX, minY, maxX, maxY)
	}
}

func TestDefaultFormBuildsValidConfigAfterURL(t *testing.T) {
	a := &application{form: defaultForm()}
	a.form.URL = "https://example.invalid/"
	c, err := a.config()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxDepth != 3 || c.MaxPages != 500 || c.Cookie != "" || c.FilterLanguages {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	a.form.Pages = "abc"
	if _, err := a.config(); err == nil || !strings.Contains(err.Error(), "最大页数") {
		t.Fatalf("missing numeric validation: %v", err)
	}
}

func TestDefaultOutputFollowsExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	want := filepath.Join(filepath.Dir(exe), "downloads")
	form := defaultForm()
	if form.Output != want {
		t.Fatalf("default output = %q, want %q", form.Output, want)
	}
	other := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if got := defaultForm().Output; got != want {
		t.Fatalf("working directory changed default output: %q, want %q", got, want)
	}
}

func TestImportedConfigClearsCookie(t *testing.T) {
	a := &application{form: defaultForm()}
	a.form.URL = "https://example.invalid/"
	a.form.Cookie = "session=private"
	c, err := a.config()
	if err != nil {
		t.Fatal(err)
	}
	a.applyConfig(c)
	if a.form.Cookie != "" {
		t.Fatal("import retained credentials")
	}
}

func TestDomainAutoRestoreAndMultipleManualDomains(t *testing.T) {
	a := &application{form: defaultForm(), domainAuto: true}
	a.form.URL = "https://www.example.com/docs"
	a.onURLChanged()
	if a.form.Domains != "*.example.com" {
		t.Fatal(a.form.Domains)
	}
	a.form.Domains = "*.example.com; cdn.example.net"
	a.onDomainChanged()
	a.form.URL = "https://www.other.org/"
	a.onURLChanged()
	if a.domainAuto || a.form.Domains != "*.example.com; cdn.example.net" {
		t.Fatal("manual multi-domain rule overwritten")
	}
	a.enableDomainAuto()
	if !a.domainAuto || a.form.Domains != "*.other.org" {
		t.Fatalf("auto restore: %q", a.form.Domains)
	}
}

func TestLanguageFilterRoundTrip(t *testing.T) {
	a := &application{form: defaultForm()}
	a.form.URL = "https://example.invalid/en/"
	a.form.FilterLanguages = true
	cfg, err := a.config()
	if err != nil || !cfg.FilterLanguages {
		t.Fatalf("filter not applied: %+v, %v", cfg, err)
	}
	a.applyConfig(cfg)
	if !a.form.FilterLanguages {
		t.Fatal("applying config lost language filter")
	}
	file := filepath.Join(t.TempDir(), "settings.json")
	if err := clone.SaveConfig(file, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clone.LoadConfig(file)
	if err != nil || !loaded.FilterLanguages {
		t.Fatalf("saved config lost language filter: %+v, %v", loaded, err)
	}
}

func TestDomainAutofillAndManualOverride(t *testing.T) {
	a := &application{form: defaultForm(), domainAuto: true}
	a.form.URL = "https://www.example.com.cn:8443/docs"
	a.onURLChanged()
	if a.form.Domains != "*.example.com.cn" {
		t.Fatalf("domain not inferred as wildcard: %+v", a.form)
	}
	a.form.Domains = "custom.example.org"
	a.onDomainChanged()
	a.form.URL = "https://other.invalid/"
	a.onURLChanged()
	if a.form.Domains != "custom.example.org" {
		t.Fatal("manual rule overwritten")
	}
	a.form.Domains = ""
	a.onDomainChanged()
	if a.form.Domains != "*.other.invalid" {
		t.Fatalf("auto root wildcard domain not restored: %q", a.form.Domains)
	}
}

func TestSettingsPathsRoundTrip(t *testing.T) {
	a := &application{form: defaultForm(), domainAuto: true}
	a.form.URL = "https://example.invalid/"
	a.onURLChanged()
	a.form.FilePaths.Picture = "/assets/photos/"
	cfg, err := a.config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FilePaths.Picture != "/assets/photos/" || cfg.Domains != "*.example.invalid" {
		t.Fatalf("configuration lost: %+v", cfg)
	}
	a.applyConfig(clone.DefaultConfig())
	if a.form.FilePaths.Picture != "/static/picture/" {
		t.Fatal("restore defaults lost")
	}
	a.applyConfig(cfg)
	if a.form.FilePaths.Picture != "/assets/photos/" || a.form.Domains != "*.example.invalid" {
		t.Fatal("import settings lost")
	}
}

func TestSidebarPagesRender(t *testing.T) {
	a := &application{form: defaultForm(), status: "就绪"}
	tester := ui.NewTester(a.view, 1180, 820)
	for _, label := range []string{"网站克隆工具", "新建任务", "文件路径", "请求设置", "下载进度", "任务历史", "目标、输出与抓取范围", "开始下载"} {
		if !tester.HasText(label) {
			t.Errorf("missing control: %s", label)
		}
	}
	checks := map[page][]string{
		pagePaths:    {"文件结构路径", "恢复默认"},
		pageRequest:  {"UA 预设", "Google", "Baidu", "Bing", "清空"},
		pageProgress: {"任务状态", "取消任务", "续传失败项", "运行日志"},
		pageHistory:  {"暂无任务记录"},
		pageHelp:     {"并发、重试与续传"},
	}
	for p, labels := range checks {
		a.page = p
		tester.Frame()
		for _, label := range labels {
			if !tester.HasText(label) {
				t.Errorf("page %s missing %s", pageNames[p], label)
			}
		}
	}
	tester.SetSize(860, 560)
	if !tester.HasText("网站克隆工具") || !tester.HasText("下载速度：0 B/s") {
		t.Fatal("sidebar or status bar lost after resize")
	}
}

func contrastRatio(a, b ui.Color) float64 {
	luminance := func(c ui.Color) float64 {
		channel := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.04045 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
	}
	one, two := luminance(a), luminance(b)
	if one < two {
		one, two = two, one
	}
	return (one + 0.05) / (two + 0.05)
}

func TestFlatThemeUsesReadableContrast(t *testing.T) {
	if ratio := contrastRatio(colorTitle, colorCard); ratio < 7 {
		t.Fatalf("title contrast too low: %.2f", ratio)
	}
	if ratio := contrastRatio(colorInk, colorCard); ratio < 4.5 {
		t.Fatalf("body contrast too low: %.2f", ratio)
	}
	if ratio := contrastRatio(colorSideDim, colorSide); ratio < 4.5 {
		t.Fatalf("sidebar hint contrast too low: %.2f", ratio)
	}
}

func TestSidebarSelectionKeepsTextAligned(t *testing.T) {
	a := &application{form: defaultForm(), status: "就绪"}
	tester := ui.NewTester(a.view, 1180, 820)
	selected, ok := tester.Find("新建任务")
	if !ok {
		t.Fatal("selected navigation label missing")
	}
	if err := tester.Click("文件路径"); err != nil {
		t.Fatal(err)
	}
	if a.page != pagePaths {
		t.Fatal("sidebar navigation did not switch pages")
	}
	unselected, ok := tester.Find("新建任务")
	if !ok {
		t.Fatal("inactive navigation label missing")
	}
	if selected.X != unselected.X {
		t.Fatalf("selection shifted text: selected x=%v, inactive x=%v", selected.X, unselected.X)
	}
}

func TestDownloadLimitsStayGroupedAtEachWidth(t *testing.T) {
	for _, width := range []int{1180, 760, 375} {
		a := &application{form: defaultForm(), page: pageRequest, status: "就绪"}
		tester := ui.NewTester(a.view, width, 820)
		labels := []string{"资源上限", "间隔 / 毫秒", "单文件 / MB", "总量 / MB"}
		var positions [4]ui.Rect
		for i, label := range labels {
			r, ok := tester.Find(label)
			if !ok {
				t.Fatalf("%dpx limits missing: %s", width, label)
			}
			positions[i] = r
		}
		if width >= 520 {
			if positions[0].Y != positions[1].Y || positions[2].Y != positions[3].Y || positions[2].Y <= positions[0].Y || positions[0].X != positions[2].X || positions[1].X != positions[3].X {
				t.Fatalf("%dpx limits not in aligned 2x2 grid: %+v", width, positions)
			}
		} else {
			for i := 1; i < len(positions); i++ {
				if positions[i].X != positions[0].X || positions[i].Y <= positions[i-1].Y {
					t.Fatalf("%dpx limits not in aligned single column: %+v", width, positions)
				}
			}
		}
		if a.pageScroll[pageRequest].MaxX > 1 {
			t.Fatalf("%dpx limits overflow horizontally: %.1f", width, a.pageScroll[pageRequest].MaxX)
		}
	}
}

func TestBrowserImportAndMultiDomainControlsRender(t *testing.T) {
	for _, size := range [][2]int{{1180, 820}, {375, 720}} {
		a := &application{form: defaultForm(), status: "就绪", domainAuto: true, browserChoice: "Google Chrome"}
		a.form.URL = "https://www.example.com/"
		a.onURLChanged()
		tester := ui.NewTester(a.view, size[0], size[1])
		for _, label := range []string{"限定域名", "自动识别", "限定路径", "排除路径"} {
			if !tester.HasText(label) {
				t.Errorf("%v task missing %s", size, label)
			}
		}
		a.page = pageRequest
		tester.Frame()
		for _, label := range []string{"Cookie", "User-Agent", "从浏览器导入", "Google Chrome", "打开登录浏览器", "读取 Cookie 和 UA"} {
			if !tester.HasText(label) {
				t.Errorf("%v request missing %s", size, label)
			}
		}
	}
}

func TestThemeUsesChineseUIFontFallback(t *testing.T) {
	a := &application{form: defaultForm(), status: "就绪"}
	tester := ui.NewTester(a.view, 1180, 820)
	if tester == nil {
		t.Fatal("view did not initialize")
	}
	if !strings.Contains(uiFontFamily, "system-ui") || !strings.Contains(uiFontFamily, "Microsoft YaHei UI") {
		t.Fatal("font fallback chain incomplete")
	}
}

func TestTaskNumericFieldsUseBalancedColumns(t *testing.T) {
	for _, tc := range []struct {
		width, height int
	}{
		{1180, 820},
		{760, 720},
		{375, 720},
	} {
		a := &application{form: defaultForm(), status: "就绪"}
		tester := ui.NewTester(a.view, tc.width, tc.height)
		labels := []string{"最大深度", "最大页数", "下载超时 /秒", "编码转换"}
		var positions [4]ui.Rect
		for i, label := range labels {
			r, ok := tester.Find(label)
			if !ok {
				t.Fatalf("%dpx missing field %q", tc.width, label)
			}
			positions[i] = r
		}
		switch tc.width {
		case 1180:
			for i := 1; i < len(positions); i++ {
				if positions[i].Y != positions[0].Y || positions[i].X <= positions[i-1].X {
					t.Fatalf("desktop fields not aligned on one row: %+v", positions)
				}
			}
		case 760:
			if positions[0].Y != positions[1].Y || positions[2].Y != positions[3].Y || positions[0].X != positions[2].X || positions[1].X != positions[3].X {
				t.Fatalf("medium fields not aligned in two rows: %+v", positions)
			}
		default:
			for i := 1; i < len(positions); i++ {
				if positions[i].X != positions[0].X || positions[i].Y <= positions[i-1].Y {
					t.Fatalf("compact fields not aligned in one column: %+v", positions)
				}
			}
		}
		if a.pageScroll[pageTask].MaxX > 1 {
			t.Fatalf("%dpx task form overflows horizontally: %.1f", tc.width, a.pageScroll[pageTask].MaxX)
		}
	}
}

func TestTaskFieldsOrderAndAlignment(t *testing.T) {
	for _, size := range [][2]int{{1180, 820}, {375, 720}} {
		a := &application{form: defaultForm(), status: "就绪"}
		tester := ui.NewTester(a.view, size[0], size[1])
		labels := []string{"下载网址", "限定域名", "限定路径", "排除路径", "保存目录"}
		var previousY float32 = -1
		var firstX float32
		for i, label := range labels {
			r, ok := tester.Find(label)
			if !ok {
				t.Fatalf("%dpx missing field %q", size[0], label)
			}
			if r.Y <= previousY {
				t.Fatalf("%dpx %s should follow previous field", size[0], label)
			}
			if i == 0 {
				firstX = r.X
			} else if r.X != firstX {
				t.Fatalf("%dpx %s label x=%v, want %v", size[0], label, r.X, firstX)
			}
			previousY = r.Y
		}
		if a.pageScroll[pageTask].MaxX > 1 {
			t.Fatalf("%dpx task form overflows horizontally: %v", size[0], a.pageScroll[pageTask].MaxX)
		}
	}
}

func TestTaskSettingsAndProgressControls(t *testing.T) {
	a := &application{form: defaultForm(), status: "就绪"}
	tester := ui.NewTester(a.view, 1440, 860)
	for _, label := range []string{"目标、输出与抓取范围", "下载网址", "保存目录", "限定域名", "过滤其他语言目录（自动识别 /en/、/fr/ 等；保留起始语言）", "最大深度", "最大页数", "下载超时 /秒", "编码转换", "开始下载"} {
		if !tester.HasText(label) {
			t.Errorf("task workflow missing: %s", label)
		}
	}
	a.page = pageRequest
	tester.Frame()
	for _, label := range []string{"请求身份", "下载策略", "并发数", "失败重试", "下载限制", "资源上限", "间隔 / 毫秒", "单文件 / MB", "总量 / MB"} {
		if !tester.HasText(label) {
			t.Errorf("request page missing: %s", label)
		}
	}
	a.pageScroll[pageRequest].Y = 100000
	tester.Frame()
	if !tester.HasText("资源上限") || !tester.HasText("总量 / MB") || tester.HasText("展开下载限制") {
		t.Fatal("download limits should always be shown without a toggle")
	}
	if tester.HasText("下载超时 /秒") || tester.HasText("编码转换") {
		t.Fatal("timeout or encoding setting duplicated in request page")
	}
	a.page = pageProgress
	tester.Frame()
	for _, label := range []string{"任务状态", "下载进度", "下载结果", "运行日志", "新建任务"} {
		if !tester.HasText(label) {
			t.Errorf("progress workflow missing: %s", label)
		}
	}
}

func TestCompactNavigationAndForms(t *testing.T) {
	a := &application{form: defaultForm(), status: "就绪", mobileMenu: true}
	tester := ui.NewTester(a.view, 375, 720)
	for _, label := range []string{"菜单", "新建任务", "任务历史", "使用说明", "下载网址", "导入网址", "保存目录", "选择文件夹", "开始下载"} {
		if !tester.HasText(label) {
			t.Errorf("compact view missing %s", label)
		}
	}
	a.page = pageRequest
	tester.Frame()
	if !tester.HasText("UA 预设") || !tester.HasText("快捷来源") {
		t.Fatal("compact request settings not accessible")
	}
	a.page = pageProgress
	tester.Frame()
	if !tester.HasText("任务状态") || !tester.HasText("运行日志") {
		t.Fatal("compact progress view not accessible")
	}
}

func TestUAPresetAndEncoding(t *testing.T) {
	a := &application{form: defaultForm(), domainAuto: true}
	if a.form.UAPreset != "电脑 Chrome" || a.form.Encoding != encodingKeep {
		t.Fatalf("unexpected defaults: %+v", a.form)
	}
	a.form.UAPreset = "iPhone Safari"
	a.onUAPresetChanged()
	if !strings.Contains(a.form.UserAgent, "iPhone") {
		t.Fatal("preset not applied")
	}
	a.form.UserAgent = "custom-agent"
	a.onUAChanged()
	if a.form.UAPreset != uaCustom {
		t.Fatal("custom UA not detected")
	}
	a.form.URL = "https://example.invalid/"
	a.onURLChanged()
	a.form.Encoding = encodingUTF8
	a.form.Concurrency = "8"
	cfg, err := a.config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Encoding != "utf-8" || cfg.Concurrency != 8 || cfg.UserAgent != "custom-agent" {
		t.Fatalf("config mapping lost: %+v", cfg)
	}
	a.applyConfig(cfg)
	if a.form.Encoding != encodingUTF8 || a.form.Concurrency != "8" {
		t.Fatal("applyConfig lost new fields")
	}
	a.form.Concurrency = "99"
	if _, err := a.config(); err == nil {
		t.Fatal("invalid concurrency accepted")
	}
}

func TestHistoryUpsertAndDelete(t *testing.T) {
	a := &application{form: defaultForm()}
	a.recordHistory(clone.HistoryEntry{ID: "1", URL: "https://a.invalid/", Status: "已完成"})
	a.recordHistory(clone.HistoryEntry{ID: "2", URL: "https://b.invalid/", Status: "部分失败", Resumable: true})
	a.recordHistory(clone.HistoryEntry{ID: "1", URL: "https://a.invalid/", Status: "已取消"})
	if len(a.history) != 2 || a.history[0].ID != "1" || a.history[0].Status != "已取消" {
		t.Fatalf("upsert order wrong: %+v", a.history)
	}
	a.page = pageHistory
	tester := ui.NewTester(a.view, 1180, 820)
	if !tester.HasText("https://b.invalid/") || !tester.HasText("续传") {
		t.Fatal("history entries not rendered")
	}
	a.deleteHistory("2")
	if len(a.history) != 1 {
		t.Fatal("delete failed")
	}
}
