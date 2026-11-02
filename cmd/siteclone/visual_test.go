package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"siteclone/internal/clone"
)

func TestViewRendersAtDesktopAndCompactWidths(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		page          page
	}{
		{"desktop-task", 1180, 820, pageTask},
		{"wide-task", 1440, 860, pageTask},
		{"compact-task", 375, 720, pageTask},
		{"desktop-request", 1180, 820, pageRequest},
		{"desktop-request-limits", 1180, 820, pageRequest},
		{"medium-request-limits", 760, 720, pageRequest},
		{"compact-request", 375, 720, pageRequest},
		{"compact-request-bottom", 375, 720, pageRequest},
		{"desktop-progress", 1180, 820, pageProgress},
		{"compact-progress", 375, 720, pageProgress},
		{"desktop-history", 1180, 820, pageHistory},
		{"compact-history", 375, 720, pageHistory},
		{"compact-menu", 375, 560, pageTask},
		{"compact-task-bottom", 375, 720, pageTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &application{form: defaultForm(), status: "就绪", page: tc.page, logFilter: "全部日志", historyFilter: "全部任务"}
			if tc.page == pageHistory {
				for i := 0; i < 18; i++ {
					a.history = append(a.history, clone.HistoryEntry{ID: fmt.Sprint(i), URL: fmt.Sprintf("https://example.invalid/docs/%d", i), Status: "已完成", Started: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Pages: 12, Assets: 86})
				}
			}
			if tc.name == "compact-menu" {
				a.mobileMenu = true
			}
			tester := ui.NewTester(a.view, tc.width, tc.height)
			if tc.name == "compact-task-bottom" {
				a.pageScroll[pageTask].Y = 100000
				tester.Frame()
			}
			if tc.name == "compact-request-bottom" || tc.name == "desktop-request-limits" || tc.name == "medium-request-limits" {
				a.pageScroll[pageRequest].Y = 100000
				tester.Frame()
			}
			img := tester.Image()
			if img == nil || img.Bounds().Dx() != tc.width || img.Bounds().Dy() != tc.height {
				t.Fatal("view did not render at requested size")
			}
			if dir := os.Getenv("SITECLONE_SNAPSHOT_DIR"); dir != "" {
				file, err := os.Create(filepath.Join(dir, tc.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(file, img); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
