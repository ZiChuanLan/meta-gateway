package main

import (
	"fmt"
	"github.com/lan/meta-gateway/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("LOAD ERROR:", err)
		return
	}
	fmt.Printf("OK sticky=%v ttl=%v\n", cfg.StickyEnabled, cfg.StickyTTL)
}
