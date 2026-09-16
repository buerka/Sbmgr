package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
)

func subscriptionDeliveryLink(s *State, userName, deviceName string) (string, error) {
	u := findUser(s, userName)
	if u == nil {
		return "", errors.New("用户不存在")
	}
	d := findDevice(u, deviceName)
	if d == nil {
		return "", errors.New("设备不存在")
	}
	if !s.Subscription.Enabled {
		return "", errors.New("订阅服务未开启，请先完成 HTTPS/服务设置")
	}
	if err := subscriptionDeviceAvailable(*u, *d, time.Now()); err != nil {
		return "", err
	}
	if d.SubscriptionToken == "" {
		return "", errors.New("设备订阅尚未初始化，请刷新后重试")
	}
	return subscriptionURL(s, *d), nil
}

func (m tuiModel) deliverSubscription(user, device string, save bool) (tea.Model, tea.Cmd) {
	// Reload under the state lock at the moment of delivery so a concurrent
	// token rotation cannot copy an obsolete credential from a TUI snapshot.
	if save {
		return m.startAction("正在保存订阅链接", func(a *app) error {
			return a.withStateLock(func() error {
				s, err := loadState(a.statePath)
				if err != nil {
					return err
				}
				link, err := subscriptionDeliveryLink(s, user, device)
				if err != nil {
					return err
				}
				dir := filepath.Join(filepath.Dir(a.statePath), "exports")
				if err := os.MkdirAll(dir, 0700); err != nil {
					return err
				}
				f, err := os.CreateTemp(dir, "subscription-link-*.txt")
				if err != nil {
					return err
				}
				if err = f.Chmod(0600); err == nil {
					_, err = f.WriteString(link + "\n")
				}
				err = errors.Join(err, f.Sync(), f.Close())
				if err != nil {
					_ = os.Remove(f.Name())
					return errors.New("订阅链接保存失败")
				}
				fmt.Fprintln(a.out, "已保存订阅链接：", f.Name())
				return nil
			})
		})
	}
	var link string
	var err error
	if m.a != nil {
		err = m.a.withStateLock(func() error {
			s, e := loadState(m.a.statePath)
			if e != nil {
				return e
			}
			link, e = subscriptionDeliveryLink(s, user, device)
			return e
		})
	} else {
		link, err = subscriptionDeliveryLink(m.state, user, device)
	}
	if err != nil {
		m.status, m.statusError = err.Error(), true
		return m, nil
	}
	m.status, m.statusError = "已发送复制请求；终端需支持 OSC52，未复制时可用“保存链接”", false
	return m, tea.SetClipboard(link)
}

func subscriptionActionEntries() []tuiMenuEntry {
	return []tuiMenuEntry{
		{title: "复制订阅地址", description: "发送到本地剪贴板；SSH 终端需支持 OSC52"},
		{title: "保存订阅地址", description: "写入 exports 私有文本文件，便于取回与分发"},
		{title: "显示二维码", description: "供客户端扫码导入本设备订阅"},
	}
}

func (m tuiModel) renderSubscriptionActions() string {
	return m.renderMenuPage("订阅交付", m.qrUser+" / "+m.qrDevice, "立即交付，无需应用配置；默认隐藏凭据。", subscriptionActionEntries(), m.footer("↑↓ 选择", "enter 执行", "c 复制链接", "w 保存链接", "z 二维码", "esc 返回"))
}

func (m tuiModel) updateSubscriptionActions(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q", "backspace":
		m.mode = m.qrReturnMode
	case "up", "k":
		m.menuCursor = max(0, m.menuCursor-1)
	case "down", "j":
		m.menuCursor = min(2, m.menuCursor+1)
	case "c":
		return m.deliverSubscription(m.qrUser, m.qrDevice, false)
	case "w":
		return m.deliverSubscription(m.qrUser, m.qrDevice, true)
	case "z":
		m.mode = tuiQRCode
	case "enter":
		if m.menuCursor == 2 {
			m.mode = tuiQRCode
		} else {
			return m.deliverSubscription(m.qrUser, m.qrDevice, m.menuCursor == 1)
		}
	}
	return m, nil
}
