// Package tts wraps the sherpa-onnx offline TTS engine for
// Matcha-TTS + vocos Chinese speech synthesis.
//
// This file is a Go port of the Android app's TextNormalizer
// (android/app/.../tts/TextNormalizer.kt). Matcha-TTS does not convert
// Arabic numerals to Chinese readings on its own, so we pre-normalize the
// text before synthesis: dates, times, temperatures, percentages, ranges,
// decimals and standalone integers are all turned into Chinese readings.
package tts

import (
	"regexp"
	"strconv"
	"strings"
)

// NormalizeEnabled toggles text normalization. Set to false to send raw
// text straight to the TTS engine.
var NormalizeEnabled = true

var chineseDigits = [...]rune{'零', '一', '二', '三', '四', '五', '六', '七', '八', '九'}
var units = [...]rune{0, '十', '百', '千'}
var wanUnits = [...]rune{0, '万', '亿'}

// Normalize converts text for TTS: Arabic numerals → Chinese readings.
//
// The replacement order matches the Android TextNormalizer exactly —
// more specific patterns (ranges, units, dates, times) are replaced before
// the catch-all standalone-integer and English passes.
func Normalize(text string) string {
	if !NormalizeEnabled {
		return text
	}

	result := text

	// Range with ℃: 28℃～35℃, 28~35℃ → 二十八至三十五摄氏度
	result = reRangeCelsius.ReplaceAllStringFunc(result, func(m string) string {
		g := reRangeCelsius.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "至" + numberToChinese(g[2]) + "摄氏度"
	})

	// Range with 度: 28~35度 → 二十八至三十五度
	result = reRangeDegree.ReplaceAllStringFunc(result, func(m string) string {
		g := reRangeDegree.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "至" + numberToChinese(g[2]) + "度"
	})

	// Celsius: 35℃ → 三十五摄氏度
	result = reCelsius.ReplaceAllStringFunc(result, func(m string) string {
		g := reCelsius.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "摄氏度"
	})

	// Percentage: 50% → 百分之五十
	result = rePercent.ReplaceAllStringFunc(result, func(m string) string {
		g := rePercent.FindStringSubmatch(m)
		return "百分之" + numberToChinese(g[1])
	})

	// Range: 28~35 → 二十八至三十五
	result = reRange.ReplaceAllStringFunc(result, func(m string) string {
		g := reRange.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "至" + numberToChinese(g[2])
	})

	// Year: 2026年 → 二零二六年
	result = reYear.ReplaceAllStringFunc(result, func(m string) string {
		g := reYear.FindStringSubmatch(m)
		return digitsToChinese(g[1]) + "年"
	})

	// Month: 7月 → 七月
	result = reMonth.ReplaceAllStringFunc(result, func(m string) string {
		g := reMonth.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "月"
	})

	// Day: 2日 → 二日, 31日 → 三十一日
	result = reDay.ReplaceAllStringFunc(result, func(m string) string {
		g := reDay.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "日"
	})

	// Hour: 14点 → 十四点
	result = reHour.ReplaceAllStringFunc(result, func(m string) string {
		g := reHour.FindStringSubmatch(m)
		return numberToChinese(g[1]) + "点"
	})

	// Time: 17:00 → 十七点, 19:35 → 十九点三十五分. Must run before the
	// standalone-integer pass, otherwise "17" is already rewritten.
	result = replaceTime(result)

	// Decimal: 3.14 → 三点一四
	result = reDecimal.ReplaceAllStringFunc(result, func(m string) string {
		g := reDecimal.FindStringSubmatch(m)
		intPart := numberToChinese(g[1])
		var frac strings.Builder
		for _, c := range g[2] {
			frac.WriteRune(chineseDigits[c-'0'])
		}
		return intPart + "点" + frac.String()
	})

	// Remaining standalone integers (quantity reading): 38 → 三十八
	result = reStandaloneInt.ReplaceAllStringFunc(result, func(m string) string {
		return numberToChinese(m)
	})

	// Remaining English: letter-by-letter pronunciation
	result = reEnglish.ReplaceAllStringFunc(result, func(m string) string {
		var sb strings.Builder
		for _, c := range m {
			sb.WriteString(englishLetterToChinese(c))
		}
		return sb.String()
	})

	// Collapse multiple spaces.
	result = reWhitespace.ReplaceAllString(result, "")

	return result
}

// replaceTime converts HH:MM to Chinese, skipping matches followed by a
// digit (the RE2 equivalent of the Kotlin `(?!\d)` lookahead).
func replaceTime(s string) string {
	locs := reTime.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}

	var sb strings.Builder
	last := 0
	for _, loc := range locs {
		matchStart, matchEnd := loc[0], loc[1]
		// Negative lookahead: if a digit immediately follows, skip.
		if matchEnd < len(s) && s[matchEnd] >= '0' && s[matchEnd] <= '9' {
			continue
		}
		sb.WriteString(s[last:matchStart])
		hour := s[loc[2]:loc[3]]
		minute := s[loc[4]:loc[5]]
		sb.WriteString(formatTime(hour, minute))
		last = matchEnd
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// formatTime renders an HH:MM time in spoken Chinese.
func formatTime(hourStr, minuteStr string) string {
	hour := numberToChinese(hourStr)
	minute, err := strconv.Atoi(minuteStr)
	if err != nil {
		minute = -1
	}
	switch {
	case minute == 0:
		return hour + "点"
	case minute > 0 && minute < 10:
		return hour + "点零" + numberToChinese(minuteStr) + "分"
	default:
		return hour + "点" + numberToChinese(minuteStr) + "分"
	}
}

// numberToChinese reads a number as a quantity: 38 → 三十八, 1520 → 一千五百二十.
func numberToChinese(numStr string) string {
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		// Too large (or not a plain integer): read digit by digit.
		return digitsToChinese(numStr)
	}
	if n == 0 {
		return "零"
	}
	if n < 10 {
		return string(chineseDigits[n])
	}

	l := len(numStr)
	// Group from the right in chunks of 4 digits (个十百千 / 万 / 亿). The
	// most-significant group may be shorter than 4. This mirrors the Android
	// logic but aligns groups from the right so 5-7 digit numbers also read
	// correctly (e.g. 12345 → 一万二千三百四十五).
	firstLen := l % 4
	if firstLen == 0 {
		firstLen = 4
	}

	var sb strings.Builder
	for start := 0; start < l; {
		groupLen := firstLen
		if start > 0 {
			groupLen = 4
		}
		group := readGroup(numStr[start : start+groupLen])
		if group != "" {
			sb.WriteString(group)
			wanIdx := (l - (start + groupLen)) / 4
			if wanIdx > 0 {
				sb.WriteRune(wanUnits[wanIdx])
			}
		}
		start += groupLen
	}

	result := sb.String()
	// 一十 → 十 (10 → 十, 11 → 十一, 12 → 十二, ...)
	if strings.HasPrefix(result, "一十") {
		result = result[len("一"):]
	}
	return result
}

// readGroup renders a 1-4 digit group (千百十个) into Chinese, handling
// interior zeros: 1002 → 一千零二, 1020 → 一千零二十, 1200 → 一千二百.
func readGroup(group string) string {
	glen := len(group)
	var sb strings.Builder
	lastZero := false // whether the last written rune was 零

	for i := 0; i < glen; i++ {
		d := group[i] - '0'
		if d == 0 {
			allZeroAfter := true
			for j := i + 1; j < glen; j++ {
				if group[j] != '0' {
					allZeroAfter = false
					break
				}
			}
			if !allZeroAfter && sb.Len() > 0 && !lastZero {
				sb.WriteRune('零')
				lastZero = true
			}
			continue
		}
		sb.WriteRune(chineseDigits[d])
		unitIdx := glen - i - 1
		if unitIdx > 0 {
			sb.WriteRune(units[unitIdx])
		}
		lastZero = false
	}

	// Strip trailing 零.
	result := sb.String()
	result = strings.TrimRight(result, "零")
	return result
}

// digitsToChinese reads each digit individually: 2025 → 二零二五.
func digitsToChinese(s string) string {
	var sb strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			sb.WriteRune(chineseDigits[c-'0'])
		} else {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

// englishLetterToChinese maps an ASCII letter to a Chinese homophone.
func englishLetterToChinese(c rune) string {
	switch c {
	case 'a', 'A':
		return "诶"
	case 'b', 'B':
		return "必"
	case 'c', 'C':
		return "西"
	case 'd', 'D':
		return "地"
	case 'e', 'E':
		return "亿"
	case 'f', 'F':
		return "爱夫"
	case 'g', 'G':
		return "记"
	case 'h', 'H':
		return "爱尺"
	case 'i', 'I':
		return "啊亿"
	case 'j', 'J':
		return "这"
	case 'k', 'K':
		return "凯"
	case 'l', 'L':
		return "爱欧"
	case 'm', 'M':
		return "爱姆"
	case 'n', 'N':
		return "恩"
	case 'o', 'O':
		return "欧"
	case 'p', 'P':
		return "批"
	case 'q', 'Q':
		return "克欧"
	case 'r', 'R':
		return "阿儿"
	case 's', 'S':
		return "爱斯"
	case 't', 'T':
		return "替"
	case 'u', 'U':
		return "优"
	case 'v', 'V':
		return "威"
	case 'w', 'W':
		return "搭不溜"
	case 'x', 'X':
		return "艾克斯"
	case 'y', 'Y':
		return "外"
	case 'z', 'Z':
		return "贼"
	default:
		return ""
	}
}

// Pre-compiled regexes (order matters — see Normalize).
var (
	reRangeCelsius  = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[~～\-—]\s*(\d+(?:\.\d+)?)\s*℃`)
	reRangeDegree   = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[~～\-—]\s*(\d+(?:\.\d+)?)\s*度`)
	reCelsius       = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*℃`)
	rePercent       = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`)
	reRange         = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[~～\-—−]\s*(\d+(?:\.\d+)?)`)
	reYear          = regexp.MustCompile(`(\d{4})\s*年`)
	reMonth         = regexp.MustCompile(`(\d{1,2})\s*月`)
	reDay           = regexp.MustCompile(`(\d{1,2})\s*日`)
	reHour          = regexp.MustCompile(`(\d{1,2})\s*点`)
	reTime          = regexp.MustCompile(`(\d{1,2}):(\d{2})`)
	reDecimal       = regexp.MustCompile(`(\d+)\.(\d+)`)
	reStandaloneInt = regexp.MustCompile(`\d+`)
	reEnglish       = regexp.MustCompile(`[a-zA-Z]+`)
	reWhitespace    = regexp.MustCompile(`\s+`)
)
