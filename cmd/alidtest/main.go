package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers/providers/aliexpress"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Second); cancel() }()
	start := time.Now()
	for i := 1; i <= 2; i++ {
		d, err := aliexpress.NewProvider().GetDetails(ctx, "1005007555063400")
		if err != nil {
			fmt.Printf("try%d after %s err=%v\n", i, time.Since(start).Round(time.Second), err)
		} else {
			fmt.Printf("try%d after %s OK title=%q price=%s\n", i, time.Since(start).Round(time.Second), d.Name, d.VendorInfos[0].Price)
			return
		}
	}
}
