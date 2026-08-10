package main

import (
	"context"
	"fmt"
	"os"
	"time"

	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// statstool v3: 手动指定旧服务名 v2ray.core.app.stats.command.StatsService 查询
// 用法: statstool [host:port]  (默认 127.0.0.1:62788)
func main() {
	target := "127.0.0.1:62788"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Println("NewClient err:", err)
		os.Exit(1)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &statsService.QueryStatsRequest{Reset_: false}
	resp := new(statsService.QueryStatsResponse)
	err = conn.Invoke(ctx, "/v2ray.core.app.stats.command.StatsService/QueryStats", req, resp)
	if err != nil {
		fmt.Println("Invoke err:", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %d 条 counter\n", len(resp.GetStat()))
	for _, s := range resp.GetStat() {
		fmt.Printf("  %s = %d\n", s.GetName(), s.GetValue())
	}
}
