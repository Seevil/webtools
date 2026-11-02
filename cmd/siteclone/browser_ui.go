package main

import (
	"context"
	"errors"
	"time"
)

func (a *application) openBrowserImport() {
	if a.busy || a.browserBusy || a.pendingDialogs > 0 {
		return
	}
	if a.browserChoice == "" {
		a.browserChoice = "Google Chrome"
	}
	a.browserBusy = true
	a.browserStatus = "正在启动独立浏览器会话…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		session, err := startBrowserImport(ctx, a.browserChoice, a.form.URL)
		a.window.Update(func() {
			a.browserBusy = false
			if err != nil {
				a.browserStatus = "启动失败"
				a.failure(err)
				return
			}
			if a.browserSession != nil {
				a.browserSession.Close()
			}
			a.browserSession = session
			a.browserStatus = "浏览器已打开；登录后点击“读取 Cookie 和 UA”"
		})
	}()
}

func (a *application) readBrowserImport() {
	if a.browserBusy || a.browserSession == nil {
		return
	}
	a.browserBusy = true
	a.browserStatus = "正在读取当前目标根域 Cookie…"
	session := a.browserSession
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cookie, ua, count, err := session.read(ctx)
		a.window.Update(func() {
			a.browserBusy = false
			if err != nil {
				a.browserStatus = "读取失败"
				a.failure(err)
				return
			}
			if ua == "" {
				a.failure(errors.New("浏览器没有返回 User-Agent"))
				return
			}
			a.form.Cookie, a.form.UserAgent = cookie, ua
			a.onUAChanged()
			a.browserStatus = "已读取当前目标根域 Cookie（" + itoa(count) + " 项）和 User-Agent"
			a.appendLog("已从独立浏览器会话读取目标根域 Cookie 和 User-Agent；内容不会写入配置或历史")
		})
	}()
}

func (a *application) closeBrowserImport() {
	if a.browserSession != nil {
		a.browserSession.Close()
		a.browserSession = nil
	}
	a.browserBusy = false
	a.browserStatus = "浏览器会话已关闭"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
