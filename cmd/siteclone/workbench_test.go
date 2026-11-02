package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"siteclone/internal/clone"
)

func TestLogFiltersPauseAndBoundedHistory(t *testing.T) {
	a := &application{}
	a.appendLog("已保存：app.js")
	a.appendLog("已复用相同文件：theme.css")
	a.appendLog("请求失败：missing.png")
	a.logFilter = "仅异常"
	if lines := a.filteredLogs(); len(lines) != 1 || !strings.Contains(lines[0], "missing.png") {
		t.Fatal(lines)
	}
	a.logFilter, a.logQuery = "仅复用", "THEME"
	a.resetLogPage()
	if lines := a.filteredLogs(); len(lines) != 1 {
		t.Fatal(lines)
	}
	a.pauseLog = true
	a.appendLog("已复用相同文件：theme2.css")
	if len(a.filteredLogs()) != 1 {
		t.Fatal("paused log snapshot changed")
	}
	a.pauseLog = false
	if len(a.filteredLogs()) != 2 {
		t.Fatal("resumed log did not update")
	}
	for i := 0; i < 500; i++ {
		a.appendLog(fmt.Sprint(i))
	}
	if len(a.logs) != 400 {
		t.Fatal(len(a.logs))
	}
	index := 999
	start, end, pages := pageBounds(len(a.logs), logPageSize, &index)
	if start >= end || end != 400 || index != pages-1 {
		t.Fatal(start, end, pages, index)
	}
}

func TestHistorySearchFiltersAndPagination(t *testing.T) {
	a := &application{}
	for i := 0; i < 30; i++ {
		a.history = append(a.history, clone.HistoryEntry{ID: fmt.Sprint(i), URL: fmt.Sprintf("https://example.invalid/%d", i), Status: "已完成"})
	}
	a.history[2].Status, a.history[2].Resumable = "已取消", true
	a.historyFilter = "未完成"
	if h := a.filteredHistory(); len(h) != 1 || h[0].ID != "2" {
		t.Fatal(h)
	}
	a.historyFilter, a.historyQuery = "全部任务", "/29"
	if h := a.filteredHistory(); len(h) != 1 || h[0].ID != "29" {
		t.Fatal(h)
	}
	a.historyQuery = ""
	index := 1
	start, end, pages := pageBounds(len(a.filteredHistory()), historyPageSize, &index)
	if start != 12 || end != 24 || pages != 3 {
		t.Fatal(start, end, pages)
	}
}

func TestToolbarIsVisibleAndNavigationWorks(t *testing.T) {
	for _, size := range [][2]int{{1180, 820}, {375, 560}, {375, 720}, {860, 560}} {
		a := &application{form: defaultForm(), status: "就绪"}
		tester := ui.NewTester(a.view, size[0], size[1])
		r, ok := tester.Find("开始下载")
		if !ok || r.Y < 0 || r.Y+r.H > float32(size[1])-20 || r.X < 0 || r.X+r.W > float32(size[0]) {
			t.Fatalf("%v toolbar outside viewport: %+v", size, r)
		}
		if a.pageScroll[pageTask].MaxX > 1 {
			t.Fatalf("%v horizontal overflow: %f", size, a.pageScroll[pageTask].MaxX)
		}
		a.pageScroll[pageTask].Y = 100000
		tester.Frame()
		r, _ = tester.Find("开始下载")
		if r.Y+r.H > float32(size[1])-20 {
			t.Fatal("toolbar moved on scroll")
		}
		if size[0] < 800 {
			if err := tester.Click("菜单"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("任务历史"); err != nil {
				t.Fatal(err)
			}
			if a.page != pageHistory || a.mobileMenu {
				t.Fatal("menu navigation failed")
			}
		}
	}
}

func TestLimitedTaskNotReportedComplete(t *testing.T) {
	a := &application{}
	a.finish(clone.HistoryEntry{ID: "cap"}, clone.Result{Directory: "test", Resumable: true}, nil)
	if a.history[0].Status != "未完成" {
		t.Fatal(a.history[0])
	}
}

func BenchmarkCachedLogFilter(b *testing.B) {
	a := &application{logFilter: "仅异常"}
	for i := 0; i < 400; i++ {
		a.appendLog(fmt.Sprintf("请求失败：/asset-%d", i))
	}
	a.filteredLogs()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.filteredLogs()
	}
}
