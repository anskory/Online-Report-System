// 一次性工具：生成 bcrypt 哈希，用于手动重置 sys_user.password_hash
//
//	go run ./cmd/hashgen                  # 默认输出 admin123 的哈希
//	go run ./cmd/hashgen -p 新密码         # 输出指定密码的哈希
package main

import (
	"flag"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	pwd := flag.String("p", "admin123", "要生成哈希的明文密码")
	flag.Parse()
	if *pwd == "" {
		fmt.Println("err: 密码不能为空")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(*pwd), bcrypt.DefaultCost)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(string(h))
}
