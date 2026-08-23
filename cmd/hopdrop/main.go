// Command hopdrop 是 HopDrop 的命令行客户端。
//
// HopDrop 是一个跨设备（Android / iOS / macOS / Windows / Linux）的局域网文件
// 传输工具，类似 AirDrop：发送方主动挑选文件/文件夹推送给某台设备，接收方确认后落地。
//
// 用法：
//
//	# 在接收端启动一个常驻服务，把收到的文件落到 ./inbox
//	hopdrop recv --dir ./inbox --name MacA
//
//	# 在发送端列出局域网里的在线设备
//	hopdrop peers
//
//	# 向某台设备发送文件/文件夹（按设备名或前缀匹配，也可用 --to host:port 直连）
//	hopdrop send --to MacA ./photo.jpg ./someDir
//
// 单机联调：开两个终端，一个 recv 一个 send，即可在一台机器上验证完整链路。
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "recv":
		cmdRecv(os.Args[2:])
	case "send":
		cmdSend(os.Args[2:])
	case "peers":
		cmdPeers(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `HopDrop —— 跨设备局域网文件传输（类 AirDrop）

用法:
  hopdrop recv  --dir <下载目录> [--name <设备名>] [--port <端口>] [--yes]
  hopdrop send  [--to <设备名|host:port>] [--name <设备名>] [--timeout <秒>] <文件或目录>...
  hopdrop peers [--name <设备名>] [--timeout <秒>]

recv   常驻接收：把其他设备推送来的文件落到 <下载目录>。默认会就每次传输询问是否接收，
       加 --yes 则自动接受。
send   把一批本地文件/目录发送给某台设备。不带 --to 时会列出在线设备让你选择。
peers  仅广播并监听，打印发现到的设备，用于排查发现是否正常。
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func defaultName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "hopdrop-cli"
}
