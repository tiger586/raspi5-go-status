// 工具函數
package main

import (
	"strings"
)

// "golang.org/x/text/message"

// p := message.NewPrinter(message.MatchLanguage("en"))

// 切割字串為 N 個字符的片段
func splitByN(s string, n int) []string {
	var result []string
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		result = append(result, s[i:end])
	}
	return result
}

// 讓文字能置中，計算字串長度加入空白的字元
func testCenter(str string, width int) string {
	// 計算需要填充的空格數量
	// spaces := (width - len(str)) / 2
	// if spaces < 0 {
	// 	spaces = 0
	// }
	spaces := max((width-len(str))/2, 0)
	// 在字串前面添加空格
	return strings.Repeat(" ", spaces) + str
}
