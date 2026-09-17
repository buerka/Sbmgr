package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// batchCmd preserves all batch policy operations for automation. Pointer fields
// in the JSON format distinguish an omitted setting from an explicit reset.
func (a *app) batchCmd(args []string) error {
	fs := a.newFlagSet("batch")
	file := fs.String("file", "", "批量操作 JSON 文件（不能与其他参数混用）")
	users := fs.String("users", "", "用户名，逗号分隔；任一用户无效则全部取消")
	enabled := fs.Bool("enabled", true, "启用或停用所选用户")
	quota := fs.String("quota", "", "配额，例如 100G；0 不限")
	mode := fs.String("quota-mode", "total", "计费方向 total|upload|download")
	expire := fs.String("expire", "", "到期日 YYYY-MM-DD；空字符串清除")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("batch 不接受位置参数")
	}
	op := batchOperation{Kind: batchUserSettings, Users: strings.Split(*users, ",")}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if *file != "" {
		if len(seen) != 1 {
			return errors.New("--file 不能与其他批量参数混用")
		}
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, webMaxRequest+1))
		if err != nil {
			return err
		}
		if err := webDecode(data, &op); err != nil {
			return err
		}
	} else {
		if seen["enabled"] {
			op.User.Enabled = enabled
		}
		if seen["quota-mode"] {
			op.User.QuotaMode = mode
		}
		if seen["expire"] {
			op.User.Expires = expire
		}
		if seen["quota"] {
			n, err := parseSize(*quota)
			if err != nil {
				return err
			}
			op.User.QuotaBytes = &n
		}
	}
	return a.batchUsers(op)
}

func (a *app) clientCmd(args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return errors.New("用法: admin client set --server 主机名 --port 端口")
	}
	fs := a.newFlagSet("client set")
	server := fs.String("server", "", "客户端连接的公网地址")
	port := fs.Int("port", 0, "客户端连接端口")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *server == "" || *port == 0 {
		return errors.New("必须同时指定 --server 和 --port")
	}
	return a.setClientEndpoint(*server, *port)
}

func (a *app) auditCmd(args []string) error {
	fs := a.newFlagSet("audit")
	limit := fs.Int("limit", 100, "最近记录数，1–1000")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 1000 {
		return errors.New("audit --limit 必须为 1–1000")
	}
	records, err := readAuditRecords(a.statePath, *limit)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, string(data))
	return nil
}
