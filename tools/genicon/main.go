// genicon 生成打包用的图标。
//
//	go run ./tools/genicon                    # resources/icon.png，1024px（mygo build 用）
//	go run ./tools/genicon <输出路径> <尺寸>   # 比如 AppImage 要的 256px
package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

	"dshdesktop/internal/appicon"
)

func main() {
	out, size := filepath.Join("resources", "icon.png"), 1024
	if len(os.Args) == 3 {
		n, err := strconv.Atoi(os.Args[2])
		if err != nil || n < 16 {
			log.Fatalf("尺寸不对：%q", os.Args[2])
		}
		out, size = os.Args[1], n
	} else if len(os.Args) != 1 {
		log.Fatal("用法：genicon [输出路径 尺寸]")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, appicon.PNG(size), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (%dpx)", out, size)
}
