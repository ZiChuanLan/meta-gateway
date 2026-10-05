package main

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/proxy"
)

// connectionOptionPattern matches one entry of CONNECTION_TYPE_OPTIONS.
var connectionOptionPattern = regexp.MustCompile(
	`\{\s*value:\s*"([^"]+)",\s*label:\s*"([^"]+)",\s*group:\s*"([^"]+)"\s*\}`)

// groupLabels name the console's grouping of the type dropdown.
var groupLabels = map[string]string{
	"core":  "核心协议",
	"cn":    "国内厂商",
	"intl":  "国际厂商",
	"relay": "中继品牌",
	"other": "自定义",
}

// connectionOption is one entry of the console's type dropdown.
type connectionOption struct {
	value string
	label string
	group string
}

func genConnectionTypes(root string) (string, error) {
	brands := adapters.OpenAICompatibleBrands()

	path := filepath.Join(root, "web", "src", "connectionTypes.ts")
	raw, err := readFile(path)
	if err != nil {
		return "", err
	}
	var options []connectionOption
	for _, match := range connectionOptionPattern.FindAllStringSubmatch(raw, -1) {
		options = append(options, connectionOption{value: match[1], label: match[2], group: match[3]})
	}

	consoleValues := map[string]bool{}
	for _, opt := range options {
		consoleValues[opt.value] = true
	}

	var b strings.Builder
	b.WriteString(heading(1, "连接类型全表"))
	b.WriteString(paragraph(
		"渠道的 `type_hint`（留空时取站点的 `platform`）决定用哪个适配器与哪份供应商 profile。"+
			"控制台下拉里能选的值来自 `web/src/connectionTypes.ts`；后端把已知品牌归一到规范类型"+
			"（`adapters.CanonicalType`），例如各 OpenAI 兼容品牌都归到 `openai-compatible`。",
		"**`custom` 是唯一「已知的未知」类型**：表示「我自己接端点」，默认落到 OpenAI 形态的透传。"+
			"而未收录的手工 id 会**解析失败**——这不是漏配，是刻意不让拼错的类型静默降级成 OpenAI。",
	))

	b.WriteString(heading(2, "控制台可选类型"))
	b.WriteString(countNote(len(options), "类型"))
	var rows [][]string
	for _, opt := range options {
		rows = append(rows, []string{
			"`" + opt.value + "`",
			opt.label,
			orDash(groupLabels[opt.group]),
		})
	}
	b.WriteString(table([]string{"值", "显示名", "分组"}, rows))
	b.WriteString("\n")

	b.WriteString(heading(2, "后端已知的 OpenAI 兼容品牌"))
	b.WriteString(paragraph(
		"`adapters.OpenAICompatibleBrands()` 返回的品牌会被归一到 `openai-compatible`，" +
			"从而命中模型清单与转发解析。这里列出的是后端认可的品牌拼写。",
	))
	b.WriteString(countNote(len(brands), "品牌"))
	var brandRows [][]string
	for _, brand := range brands {
		brandRows = append(brandRows, []string{
			"`" + brand + "`",
			consolePresence(consoleValues[brand]),
		})
	}
	b.WriteString(table([]string{"品牌", "控制台下拉可选"}, brandRows))
	b.WriteString("\n")

	consoleOnly := filterUnwired(valuesOf(options))
	if len(consoleOnly) > 0 {
		b.WriteString(heading(2, "需要同步的类型"))
		b.WriteString(paragraph(
			"这些类型在下拉里能选，但后端既没有把它们归一到某个协议家族，也没有对应的供应商 profile——" +
				"选完会发现解析不到适配器。",
		))
		b.WriteString(bullet(prefixBacktick(consoleOnly)))
	} else {
		b.WriteString(heading(2, "两处一致"))
		b.WriteString(paragraph(
			"下拉里的每一个类型都能被后端解析（归一到协议家族，或有供应商 profile）。" +
				"这一节在出现差集之前不会列出任何内容，所以它空着就是好消息。",
		))
	}

	brandOnly := difference(brands, consoleValues)
	if len(brandOnly) > 0 {
		b.WriteString(heading(2, "后端认可、控制台下拉没有"))
		b.WriteString(paragraph(
			"这些品牌可以用 API 或导入方式写入，但下拉里选不到。" +
				"`custom` 与 `unknown` 是刻意的兼容拼写，不是漏配。",
		))
		b.WriteString(bullet(prefixBacktick(brandOnly)))
	}

	return b.String(), nil
}

// canonicalFamilies are the protocol families the backend resolves a value to.
// A console type that lands in one of them is wired end to end.
var canonicalFamilies = map[string]bool{
	"openai-compatible": true,
	"new-api":           true,
	"one-api":           true,
	"anthropic":         true,
	"gemini":            true,
}

// filterUnwired keeps the console types the backend cannot resolve: neither a
// canonical protocol family nor a provider profile. It is a stronger test than
// "is it in the brand list", because the core protocol types and the
// profile-backed providers are deliberately absent from that list.
func filterUnwired(values []string) []string {
	var out []string
	for _, value := range values {
		if canonicalFamilies[adapters.CanonicalType(value)] {
			continue
		}
		if _, ok := proxy.LookupProviderProfile(value); ok {
			continue
		}
		out = append(out, value)
	}
	return out
}

func valuesOf(options []connectionOption) []string {
	out := make([]string, 0, len(options))
	for _, opt := range options {
		out = append(out, opt.value)
	}
	return out
}

func consolePresence(present bool) string {
	if present {
		return "是"
	}
	return "—"
}
