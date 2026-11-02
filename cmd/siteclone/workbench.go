package main

import (
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"siteclone/internal/clone"
)

const logPageSize = 60
const historyPageSize = 12

func logProblem(line string) bool {
	return strings.Contains(line, "失败") || strings.Contains(line, "未保存") || strings.Contains(line, "错误") || strings.Contains(line, "损坏") || strings.Contains(line, "已跳过")
}

func (a *application) filteredLogs() []string {
	if a.pauseLog && a.logCache != nil {
		return a.logCache
	}
	if a.logCache != nil && a.logCacheVersion == a.logVersion && a.logCacheCount == len(a.logs) && a.logCacheQuery == a.logQuery && a.logCacheFilter == a.logFilter {
		return a.logCache
	}
	query := strings.ToLower(strings.TrimSpace(a.logQuery))
	out := make([]string, 0, len(a.logs))
	for i := len(a.logs) - 1; i >= 0; i-- {
		line := a.logs[i]
		if query != "" && !strings.Contains(strings.ToLower(line), query) {
			continue
		}
		if a.logFilter == "仅异常" && !logProblem(line) {
			continue
		}
		if a.logFilter == "仅复用" && !strings.Contains(line, "复用") {
			continue
		}
		out = append(out, line)
	}
	a.logCache = out
	a.logCacheVersion, a.logCacheCount = a.logVersion, len(a.logs)
	a.logCacheQuery, a.logCacheFilter = a.logQuery, a.logFilter
	return out
}

func pageBounds(count, size int, index *int) (int, int, int) {
	pages := max(1, (count+size-1)/size)
	*index = max(0, min(*index, pages-1))
	start := *index * size
	return start, min(count, start+size), pages
}

func (a *application) filteredHistory() []clone.HistoryEntry {
	query := strings.ToLower(strings.TrimSpace(a.historyQuery))
	out := make([]clone.HistoryEntry, 0, len(a.history))
	for _, h := range a.history {
		if query != "" && !strings.Contains(strings.ToLower(h.URL+" "+h.Directory+" "+h.Status), query) {
			continue
		}
		if a.historyFilter == "未完成" && !(h.Resumable || h.Failed > 0 || h.Status == "已中断" || h.Status == "未完成" || h.Status == "已取消") {
			continue
		}
		if a.historyFilter == "已完成" && h.Status != "已完成" {
			continue
		}
		out = append(out, h)
	}
	return out
}

func (a *application) resetLogPage() {
	a.logPage, a.logScroll.Y = 0, 0
	a.logCache = nil
}

func (a *application) exportLogs() {
	if a.pendingDialogs > 0 || a.choosingZIP || len(a.logs) == 0 {
		return
	}
	// Snapshot on the UI thread; downloads can keep adding messages while the dialog is open.
	text := strings.Join(a.logs, "\r\n") + "\r\n"
	a.pendingDialogs++
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.window, Title: "导出当前保留日志", DefaultPath: "siteclone-log.txt", Filters: []mygo.FileFilter{{Name: "文本日志", Extensions: []string{"txt"}}}})
		if err == nil && path != "" {
			err = os.WriteFile(path, []byte(text), 0600)
		}
		a.window.Update(func() {
			a.pendingDialogs--
			a.failure(err)
			if err == nil && path != "" {
				a.appendLog("日志已导出")
			}
		})
	}()
}

func (a *application) actionBar(c *ui.Context) {
	if a.page != pageTask && a.page != pageProgress && !a.busy {
		return
	}
	width, _ := c.Size()
	ui.Row(c).FillWidth().Padding(10, 16).Gap(9).Wrap().Background(colorCard).Border(1, colorLine).Children(func() {
		if width >= 800 {
			text := "目标与抓取范围 → 请求设置与下载策略"
			if a.busy || a.page == pageProgress {
				text = a.status
			}
			ui.Text(c, text).Grow(1).FontSize(12).TextColor(colorInk).SingleLine()
		}
		if a.busy {
			label := "取消任务"
			if a.cancelling {
				label = "正在整理…"
			}
			ui.Button(c, label).Disabled(a.cancelling || a.exporting).OnClick(a.cancelRun)
			if a.page != pageProgress {
				ui.PrimaryButton(c, "查看进度").OnClick(func() { a.page = pageProgress })
			}
			return
		}
		if a.page == pageTask {
			ui.Button(c, "请求设置").OnClick(func() { a.page = pageRequest })
			ui.PrimaryButton(c, "开始下载").Disabled(a.choosingZIP || a.pendingDialogs > 0).OnClick(a.start)
			return
		}
		current, ok := a.findHistory(a.currentID)
		ui.Button(c, "取消任务").Disabled(true)
		ui.Button(c, "续传失败项").Disabled(!ok || !a.result.Resumable || a.pendingDialogs > 0 || a.choosingZIP).OnClick(func() { a.resume(current) })
		ui.PrimaryButton(c, "新建任务").Disabled(a.pendingDialogs > 0 || a.choosingZIP).OnClick(func() { a.page = pageTask })
	})
}

func (a *application) logPanel(c *ui.Context) {
	a.card(c, "运行日志", func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			ui.TextInput(c, &a.logQuery).Width(180).Label("搜索日志").Placeholder("搜索关键词").OnChange(a.resetLogPage)
			ui.Select(c, &a.logFilter, []string{"全部日志", "仅异常", "仅复用"}).Width(120).Label("日志筛选").OnChange(a.resetLogPage)
			label := "暂停更新"
			if a.pauseLog {
				label = "恢复更新"
			}
			ui.Button(c, label).OnClick(func() { a.filteredLogs(); a.pauseLog = !a.pauseLog; a.logPage = 0 })
			ui.Button(c, "导出日志").Disabled(len(a.logs) == 0 || a.pendingDialogs > 0 || a.choosingZIP).OnClick(a.exportLogs)
		})
		lines := a.filteredLogs()
		start, end, pages := pageBounds(len(lines), logPageSize, &a.logPage)
		ui.Textf(c, "保留最近 400 条 · 最新在前 · 匹配 %d 条", len(lines)).FontSize(11).TextColor(colorSideDim)
		ui.Scroll(c).FillWidth().Height(230).TrackScroll(&a.logScroll).Padding(10).Gap(7).Background(colorSoft).Radius(8).Children(func() {
			if len(lines) == 0 {
				text := "暂无日志"
				if len(a.logs) > 0 {
					text = "没有匹配的日志"
				}
				ui.Text(c, text).FontSize(12).TextColor(colorInk)
			}
			for _, line := range lines[start:end] {
				ink := colorInk
				if logProblem(line) {
					ink = c.Theme().Danger
				}
				ui.Text(c, line).FontSize(12).TextColor(ink).Selectable()
			}
		})
		ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
			ui.Button(c, "较新日志").Disabled(a.logPage == 0).OnClick(func() { a.logPage--; a.logScroll.Y = 0 })
			ui.Textf(c, "%d / %d 页", a.logPage+1, pages).FontSize(12)
			ui.Button(c, "较早日志").Disabled(a.logPage+1 == pages).OnClick(func() { a.logPage++; a.logScroll.Y = 0 })
		})
	})
}
