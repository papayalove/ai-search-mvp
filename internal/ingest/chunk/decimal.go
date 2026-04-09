package chunk

import (
	"regexp"
	"strings"
)

// 与常见 Python 实现一致：用私有区字符占位，避免按句读切分时打断小数里的点。
const decimalPlaceholder = "\uE000"

var reDecimalNumber = regexp.MustCompile(`\p{Nd}+\.\p{Nd}+`)

func protectDecimalPeriods(s string) string {
	return reDecimalNumber.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ReplaceAll(m, ".", decimalPlaceholder)
	})
}

func unprotectDecimalPeriods(s string) string {
	return strings.ReplaceAll(s, decimalPlaceholder, ".")
}
