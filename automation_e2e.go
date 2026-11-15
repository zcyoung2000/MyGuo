//go:build e2e

package main

import (
	_ "github.com/egoist/mygo/plugins/automation"
	"os"
)

func init() {
	if os.Getenv("MYGUO_QR_QA") == "1" {
		initialView = "settings"
		mainWindowTitle = "扫码登录验证 · myguo"
	}
}
