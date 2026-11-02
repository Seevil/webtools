//go:build ignore

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	gdi32         = syscall.NewLazyDLL("gdi32.dll")
	shell32       = syscall.NewLazyDLL("shell32.dll")
	ole32         = syscall.NewLazyDLL("ole32.dll")
	comdlg32      = syscall.NewLazyDLL("comdlg32.dll")
	createWindow  = user32.NewProc("CreateWindowExW")
	defWindowProc = user32.NewProc("DefWindowProcW")
	sendMessage   = user32.NewProc("SendMessageW")
	setWindowText = user32.NewProc("SetWindowTextW")
	getWindowText = user32.NewProc("GetWindowTextW")
	moveWindow    = user32.NewProc("MoveWindow")
	enableWindow  = user32.NewProc("EnableWindow")
	showWindow    = user32.NewProc("ShowWindow")
	postMessage   = user32.NewProc("PostMessageW")
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type message struct {
	Window         uintptr
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type minMaxInfo struct{ Reserved, MaxSize, MaxPosition, MinTrack, MaxTrack point }
type browseInfo struct {
	Owner, Root     uintptr
	Display, Title  *uint16
	Flags           uint32
	Callback, Param uintptr
	Image           int32
}
type openFileName struct {
	Size                         uint32
	Owner, Instance              uintptr
	Filter, CustomFilter         *uint16
	MaxCustomFilter, FilterIndex uint32
	File                         *uint16
	MaxFile                      uint32
	FileTitle                    *uint16
	MaxFileTitle                 uint32
	InitialDir, Title            *uint16
	Flags                        uint32
	FileOffset, Extension        uint16
	DefaultExtension             *uint16
	CustomData, Hook             uintptr
	Template                     *uint16
	Reserved                     uintptr
	Reserved2, FlagsEx           uint32
}

func wide(s string) *uint16  { p, _ := syscall.UTF16PtrFromString(s); return p }
func signed(n int32) uintptr { return uintptr(n) }
func text(hwnd uintptr) string {
	n, _, _ := user32.NewProc("GetWindowTextLengthW").Call(hwnd)
	buf := make([]uint16, n+1)
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func setText(hwnd uintptr, s string) { setWindowText.Call(hwnd, uintptr(unsafe.Pointer(wide(s)))) }
func enabled(hwnd uintptr, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	enableWindow.Call(hwnd, v)
}
func messageBox(hwnd uintptr, title, content string, flags uintptr) uintptr {
	r, _, _ := user32.NewProc("MessageBoxW").Call(hwnd, uintptr(unsafe.Pointer(wide(content))), uintptr(unsafe.Pointer(wide(title))), flags)
	return r
}
func openExternal(hwnd uintptr, target string) error {
	r, _, _ := shell32.NewProc("ShellExecuteW").Call(hwnd, uintptr(unsafe.Pointer(wide("open"))), uintptr(unsafe.Pointer(wide(target))), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("打开失败（系统代码 %d）", r)
	}
	return nil
}
func chooseFolder(hwnd uintptr) string {
	buf := make([]uint16, 32768)
	bi := browseInfo{Owner: hwnd, Display: &buf[0], Title: wide("选择克隆文件的保存目录"), Flags: 0x41}
	pidl, _, _ := shell32.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer ole32.NewProc("CoTaskMemFree").Call(pidl)
	ok, _, _ := shell32.NewProc("SHGetPathFromIDListW").Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(bi)
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
func chooseFile(hwnd uintptr, save bool, title, extension, initial string) string {
	buf := make([]uint16, 32768)
	copy(buf, syscall.StringToUTF16(initial))
	filter := append(syscall.StringToUTF16("文件 (*."+extension+")"), syscall.StringToUTF16("*."+extension)...)
	filter = append(filter, 0)
	of := openFileName{Owner: hwnd, Filter: &filter[0], File: &buf[0], MaxFile: uint32(len(buf)), Title: wide(title), DefaultExtension: wide(extension), Flags: 0x00080000 | 0x00000008 | 0x00000800}
	of.Size = uint32(unsafe.Sizeof(of))
	proc := comdlg32.NewProc("GetOpenFileNameW")
	if save {
		proc = comdlg32.NewProc("GetSaveFileNameW")
		of.Flags |= 2
	} else {
		of.Flags |= 0x1000
	}
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&of)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(of)
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
