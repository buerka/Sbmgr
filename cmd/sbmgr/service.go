package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sbmgr/deploy"
)

func (a *app) serviceCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr service install|start|stop|restart|status")
	}
	if args[0] == "install" {
		fs := a.newFlagSet("service install")
		output := fs.String("output-dir", "", "仅导出内嵌部署文件到指定目录，不安装服务")
		singBox := fs.String("sing-box-bin", "", "sing-box 绝对路径；默认按 PATH 查找")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("install 不接受位置参数")
		}
		if *output == "" && runtime.GOOS != "linux" {
			return errors.New("systemd 安装仅支持 Linux；可用 --output-dir 检查内嵌部署文件")
		}
		home := filepath.Dir(a.statePath)
		dir := filepath.Join(home, "deploy")
		if *output != "" {
			dir = absOrOriginal(*output)
		}
		entries, err := deploy.Assets.ReadDir(".")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := deploy.Assets.ReadFile(entry.Name())
			if err != nil {
				return err
			}
			if err := atomicWrite(filepath.Join(dir, entry.Name()), data, 0700); err != nil {
				return err
			}
		}
		if *output != "" {
			fmt.Fprintln(a.out, "内嵌部署文件已导出")
			return nil
		}
		if err := installServiceExecutable(home); err != nil {
			return err
		}
		cmdArgs := []string{filepath.Join(dir, "install-systemd.sh"), "--home", home, "--component", "core"}
		if *singBox != "" {
			cmdArgs = append(cmdArgs, "--sing-box-bin", *singBox)
		}
		cmd := exec.Command("sh", cmdArgs...)
		cmd.Stdout = a.out
		cmd.Stderr = a.err
		return cmd.Run()
	}
	if runtime.GOOS != "linux" {
		return errors.New("服务管理仅支持 Linux")
	}
	if len(args) != 1 {
		return errors.New("服务操作不接受额外参数")
	}
	var command []string
	switch args[0] {
	case "start":
		command = []string{"enable", "--now", "sbmgr.service"}
	case "stop", "restart":
		command = []string{args[0], "sbmgr.service"}
	case "status":
		command = []string{"status", "--no-pager", "sbmgr.service"}
	default:
		return errors.New("未知服务操作")
	}
	cmd := exec.Command("systemctl", command...)
	cmd.Stdout = a.out
	cmd.Stderr = a.err
	return cmd.Run()
}

// Installation may copy a new executable into an empty home. Replacement of an
// installed release remains exclusively in the external deployment transaction.
func installServiceExecutable(home string) error {
	if os.Geteuid() != 0 {
		return errors.New("安装 systemd 服务需要 root")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	destination := filepath.Join(home, "sbmgr")
	current, err := os.Stat(source)
	if err != nil {
		return err
	}
	if existing, err := os.Stat(destination); err == nil {
		if os.SameFile(current, existing) {
			return nil
		}
		return errors.New("安装目录已有 sbmgr；请使用该文件安装服务，版本替换须使用外部部署脚本")
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return atomicWrite(destination, data, 0700)
}
