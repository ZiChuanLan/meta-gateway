package main

import (
	"go/ast"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// categoryPattern matches the snake_case identifiers used as error categories.
var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// catalogEntryPattern matches one line of the console's CATEGORY_TO_CLASS map.
var catalogEntryPattern = regexp.MustCompile(`^\s*([a-z0-9_]+):\s*"([a-z_]+)",`)

// classPattern matches one member of the console's ErrorClass union.
var classPattern = regexp.MustCompile(`^\s*\|\s*"([a-z_]+)"`)

type errorRow struct {
	category string
	class    string
}

func genErrorCodes(root string) (string, error) {
	catalog, err := parseErrorCatalog(filepath.Join(root, "web", "src", "errorCatalog.ts"))
	if err != nil {
		return "", err
	}
	classes, err := parseErrorClasses(filepath.Join(root, "web", "src", "errorCatalog.ts"))
	if err != nil {
		return "", err
	}
	backend, err := backendCategories(filepath.Join(root, "internal", "proxy", "proxy_classify.go"))
	if err != nil {
		return "", err
	}

	known := map[string]bool{}
	for _, row := range catalog {
		known[row.category] = true
	}
	backendSet := map[string]bool{}
	for _, name := range backend {
		backendSet[name] = true
	}

	var b strings.Builder
	b.WriteString(heading(1, "错误码与分类"))
	b.WriteString(paragraph(
		"网关的错误有两层表示：后端各服务层产出的**原始分类**（category，snake_case 标识符），"+
			"以及控制台把它归并成的**面向操作员的错误类**（class）。分类决定重试与冷却行为，"+
			"错误类决定界面上显示什么标题与修复建议。",
		"`proxy_logs` 与响应体里携带的是原始分类；控制台展示的是错误类。",
	))

	b.WriteString(heading(2, "错误类"))
	var classRows [][]string
	for _, name := range classes {
		classRows = append(classRows, []string{"`" + name + "`", classHint(name)})
	}
	b.WriteString(countNote(len(classes), "错误类"))
	b.WriteString(table([]string{"错误类", "含义"}, classRows))
	b.WriteString("\n")

	b.WriteString(heading(2, "分类 → 错误类"))
	b.WriteString(paragraph(
		"下表由 `web/src/errorCatalog.ts` 生成。它是跨服务层的分类总表——" +
			"后端新产出一个分类而这里没有对应项时，控制台会把它渲染成通用兜底文案。",
	))
	b.WriteString(countNote(len(catalog), "已知分类"))
	var catalogRows [][]string
	for _, row := range catalog {
		catalogRows = append(catalogRows, []string{"`" + row.category + "`", "`" + row.class + "`"})
	}
	b.WriteString(table([]string{"分类", "错误类"}, catalogRows))
	b.WriteString("\n")

	b.WriteString(heading(2, "转发层产出的分类"))
	b.WriteString(paragraph(
		"下表由 `internal/proxy/proxy_classify.go` 的 `return` 语句生成，是转发层实际会写进日志的分类。" +
			"它同时决定该次失败**是否可重试**：可重试的失败会累加成员失败计数并触发冷却。",
	))
	var backendRows [][]string
	for _, name := range backend {
		backendRows = append(backendRows, []string{"`" + name + "`", orDash(catalogClass(catalog, name))})
	}
	b.WriteString(table([]string{"分类", "控制台错误类"}, backendRows))
	b.WriteString("\n")

	b.WriteString(heading(3, "动态分类：`upstream_status_<code>`"))
	b.WriteString(paragraph(
		"上游返回非成功状态码时，分类不是固定字面量，而是 `upstream_status_` 加 HTTP 状态码（例如 " +
			"`upstream_status_429`）。控制台的分类表里只显式列出了需要区别对待的几个，其余按前缀规则落到同一个错误类。",
	))

	drift := difference(backend, known)
	if len(drift) > 0 {
		b.WriteString(heading(2, "需要处理的差集"))
		b.WriteString(paragraph(
			"转发层产出、但控制台分类表里**没有**对应项的分类。它们会落到通用兜底文案上，" +
				"也就是操作员看不到具体原因。",
		))
		b.WriteString(bullet(prefixBacktick(drift)))
	} else {
		b.WriteString(heading(2, "两处一致"))
		b.WriteString(paragraph(
			"转发层产出的每一个分类都能在控制台分类表里找到对应项。这一节在出现差集之前不会列出任何内容，" +
				"所以它空着就是好消息。",
		))
	}

	// The console renders a log row's error label straight from
	// `logsPage.errorClass.<class>` (Logs.tsx), so a class missing from the i18n
	// files shows up in the UI as the raw key name. That is a different failure
	// from an unclassified category, and it is invisible to the i18n parity test
	// (which only checks that zh and en agree with each other — both missing
	// passes).
	b.WriteString(heading(2, "控制台文案键覆盖"))
	translations := []struct {
		label string
		path  string
	}{
		{"zh", filepath.Join(root, "web", "src", "i18n", "zh.ts")},
		{"en", filepath.Join(root, "web", "src", "i18n", "en.ts")},
	}
	var missingTranslations []string
	var translationRows [][]string
	for _, tr := range translations {
		keys, err := parseErrorClassKeys(tr.path)
		if err != nil {
			return "", err
		}
		missing := difference(classes, keys)
		translationRows = append(translationRows, []string{
			"`" + tr.label + "`",
			itoa(len(classes)-len(missing)) + " / " + itoa(len(classes)),
			joinOrDash(prefixBacktick(missing)),
		})
		for _, name := range missing {
			missingTranslations = append(missingTranslations,
				tr.label+" 缺 `logsPage.errorClass."+name+"`")
		}
	}
	b.WriteString(paragraph(
		"日志页的错误标签直接由 `logsPage.errorClass.<错误类>` 渲染，所以错误类缺翻译时" +
			"界面上会直接显示出原始键名。注意 i18n 的 parity 测试**抓不到这种漏配**——" +
			"它只比较 zh 与 en 是否互相一致，两边一起缺也能通过。",
	))
	b.WriteString(table([]string{"语言", "已覆盖", "缺失"}, translationRows))
	b.WriteString("\n")
	if len(missingTranslations) > 0 {
		b.WriteString(bullet(missingTranslations))
	} else {
		b.WriteString(paragraph("两种语言的每一个错误类都有对应文案。"))
	}

	return b.String(), nil
}

// errorClassKeyPattern matches one `"logsPage.errorClass.<class>": "..."` entry.
var errorClassKeyPattern = regexp.MustCompile(`"logsPage\.errorClass\.([a-z_]+)"\s*:`)

// parseErrorClassKeys returns the set of error classes an i18n file translates.
func parseErrorClassKeys(path string) (map[string]bool, error) {
	raw, err := readFile(path)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, match := range errorClassKeyPattern.FindAllStringSubmatch(raw, -1) {
		keys[match[1]] = true
	}
	return keys, nil
}

func prefixBacktick(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = "`" + value + "`"
	}
	return out
}

func catalogClass(catalog []errorRow, category string) string {
	for _, row := range catalog {
		if row.category == category {
			return row.class
		}
	}
	return ""
}

func parseErrorCatalog(path string) ([]errorRow, error) {
	raw, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var rows []errorRow
	for _, line := range strings.Split(raw, "\n") {
		matches := catalogEntryPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		rows = append(rows, errorRow{category: matches[1], class: matches[2]})
	}
	return rows, nil
}

func parseErrorClasses(path string) ([]string, error) {
	raw, err := readFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var classes []string
	inUnion := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "export type ErrorClass") {
			inUnion = true
			continue
		}
		if !inUnion {
			continue
		}
		if strings.Contains(line, ";") {
			break
		}
		matches := classPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		if !seen[matches[1]] {
			seen[matches[1]] = true
			classes = append(classes, matches[1])
		}
	}
	return classes, nil
}

// backendCategories collects the string literals the forward layer returns as
// error categories. Restricting to return statements is what keeps struct tags
// and log messages out of the table.
func backendCategories(path string) ([]string, error) {
	file, _, err := parseGoFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, result := range ret.Results {
			value, isLit := stringLit(result)
			if !isLit || !categoryPattern.MatchString(value) {
				continue
			}
			if seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
		return true
	})
	sort.Strings(out)
	return out, nil
}

// classHints describe what each error class means for an operator. They are
// hand-written because the console stores them as i18n templates, not as data.
func classHint(class string) string {
	hints := map[string]string{
		"network":            "连不上上游：DNS、TLS、超时、连接被拒",
		"auth":               "凭据或令牌被上游拒绝",
		"config":             "本地的地址、配置或输入不合法",
		"upstream_shape":     "上游返回的形状不是这个协议应有的形状",
		"missing_key":        "没有可用的上游凭据",
		"missing_user_token": "缺少用户令牌（该渠道需要用户自带凭据）",
		"rate_limited":       "被上游或本地限流",
		"upstream_reject":    "上游明确拒绝了这次请求",
		"pinned_upstream":    "固定成员（`single_member_id`）不可用",
		"not_found":          "模型或资源不存在",
		"server":             "网关侧错误",
		"cancelled":          "客户端取消或超时",
		"empty_response":     "上游返回了空响应",
		"unknown":            "未归类，落到通用兜底文案",
	}
	return orDash(hints[class])
}
