//go:build ignore

package main

import (
	"testing"
	"unsafe"
)

func TestWin32ABILayout(t *testing.T) {
	for _, tc := range []struct {
		name             string
		actual, expected uintptr
	}{
		{"WNDCLASSEXW", unsafe.Sizeof(windowClass{}), 80},
		{"OPENFILENAMEW", unsafe.Sizeof(openFileName{}), 152},
		{"BROWSEINFOW", unsafe.Sizeof(browseInfo{}), 64},
		{"MSG", unsafe.Sizeof(message{}), 48},
		{"SCROLLINFO", unsafe.Sizeof(scrollInfo{}), 28},
		{"MINMAXINFO", unsafe.Sizeof(minMaxInfo{}), 40},
	} {
		if tc.actual != tc.expected {
			t.Errorf("%s: size=%d, expected=%d", tc.name, tc.actual, tc.expected)
		}
	}
}
