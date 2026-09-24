//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	ghostSWHide = 0
	ghostSWShow = 5
)

var (
	whUser32           = syscall.NewLazyDLL("user32.dll")
	whFindWindowW      = whUser32.NewProc("FindWindowW")
	whEnumChildWindows = whUser32.NewProc("EnumChildWindows")
	whGetClassNameW    = whUser32.NewProc("GetClassNameW")
	whShowWindow       = whUser32.NewProc("ShowWindow")
)

var ghostEnumChildProc uintptr

func init() {
	ghostEnumChildProc = syscall.NewCallback(ghostEnumChildCallback)
}

// ghostFixWebViewHostVisibility hides or shows WebView2 Chrome_WidgetWin children so tray hit-testing works after Ghost hide.
func ghostFixWebViewHostVisibility(show bool) {
	root := ghostFindWailsTopLevel()
	if root == 0 {
		return
	}
	cmd := uintptr(ghostSWHide)
	if show {
		cmd = ghostSWShow
	}
	ghostApplyChromeWidget(root, cmd)
	_, _, _ = whEnumChildWindows.Call(root, ghostEnumChildProc, cmd)
}

func ghostFindWailsTopLevel() uintptr {
	title, _ := syscall.UTF16PtrFromString("OneBoard Safe Paste")
	hwnd, _, _ := whFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		return hwnd
	}
	class, _ := syscall.UTF16PtrFromString("wailsWindow")
	hwnd, _, _ = whFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
	return hwnd
}

func ghostEnumChildCallback(hwnd, showCmd uintptr) uintptr {
	ghostApplyChromeWidget(hwnd, showCmd)
	_, _, _ = whEnumChildWindows.Call(hwnd, ghostEnumChildProc, showCmd)
	return 1
}

func ghostApplyChromeWidget(hwnd, showCmd uintptr) {
	var buf [256]uint16
	_, _, _ = whGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	class := syscall.UTF16ToString(buf[:])
	if strings.HasPrefix(class, "Chrome_WidgetWin") {
		_, _, _ = whShowWindow.Call(hwnd, showCmd)
	}
}
