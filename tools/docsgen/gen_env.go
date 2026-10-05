package main

import (
	"go/ast"
	"path/filepath"
	"strings"
)

// envHelperKind maps the config package's env readers to the type they produce.
// The kind is what tells an operator whether a value is a bare number, a
// duration in seconds, or a comma-separated list.
var envHelperKind = map[string]string{
	"envStr":                 "字符串",
	"envBool":                "布尔",
	"envList":                "逗号分隔列表",
	"envInt":                 "整数",
	"envIntSeconds":          "整数（秒）",
	"envDurationSeconds":     "整数（秒）",
	"envHosts":               "逗号分隔主机名",
	"envCIDRs":               "逗号分隔 CIDR",
	"envAdminTokens":         "逗号分隔令牌",
	"envModelCatalogSources": "逗号分隔目录源",
}

// directEnvReaders are read straight from the process environment.
var directEnvReaders = map[string]bool{
	"LookupEnv": true,
	"Getenv":    true,
}

type envVarRow struct {
	name  string
	kind  string
	def   string
	field string
	doc   string
}

// genEnvVars reads internal/config/config.go.
//
// The shape of Load() is what makes this three passes rather than one: it reads
// each variable into a local first, then builds the Config literal from those
// locals.
//
//	channelRetryTimes, err := envInt("CHANNEL_RETRY_TIMES", 1, 0, 5)
//	…
//	ChannelRetryTimes: channelRetryTimes,
//
// A single pass that only looked at `envXxx(...)` calls would find every
// variable name and default but could never say which struct field — and
// therefore which doc comment — belongs to it, which is exactly the column that
// makes the table worth reading.
func genEnvVars(root string) (string, error) {
	path := filepath.Join(root, "internal", "config", "config.go")
	file, _, err := parseGoFile(path)
	if err != nil {
		return "", err
	}
	fieldDocs := structFieldDocs(file, "Config")

	rows := map[string]*envVarRow{}
	ensure := func(name string) *envVarRow {
		row, ok := rows[name]
		if !ok {
			row = &envVarRow{name: name}
			rows[name] = row
		}
		return row
	}

	// Pass 1 — the env readers themselves, plus the local variable each result
	// lands in. The reader is often wrapped (`strings.TrimSpace(envStr(…))`), so
	// search the expression rather than requiring the call to be its root.
	locals := map[string]*envVarRow{}
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		name, kind, def, ok := findEnvCall(assign.Rhs[0])
		if !ok {
			return true
		}
		row := ensure(name)
		row.kind = kind
		if def != "" {
			row.def = def
		}
		for _, lhs := range assign.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" || id.Name == "err" {
				continue
			}
			locals[id.Name] = row
		}
		return true
	})

	// Pass 2 — bind those locals to struct fields, which is where the doc
	// comment lives. Handles both the local-variable form and a direct call.
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return true
		}
		var row *envVarRow
		switch value := kv.Value.(type) {
		case *ast.Ident:
			row = locals[value.Name]
		case *ast.CallExpr:
			if name, kind, def, ok := envCall(value); ok {
				row = ensure(name)
				row.kind = kind
				if def != "" {
					row.def = def
				}
			}
		}
		if row == nil {
			return true
		}
		row.field = key.Name
		if doc := fieldDocs[key.Name]; doc != "" {
			row.doc = doc
		}
		return true
	})

	// Pass 3 — anything still unrecorded: variables read inside a helper such
	// as envAdminTokens, or straight through os.LookupEnv. They have no struct
	// field, so no doc comment either, but they must not be missing from the
	// table.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, kind, def, ok := envCall(call)
		if !ok {
			return true
		}
		row := ensure(name)
		if row.kind == "" {
			row.kind = kind
		}
		if row.def == "" && def != "" {
			row.def = def
		}
		return true
	})

	if len(rows) == 0 {
		return "", nil
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sortStrings(names)

	var body [][]string
	documented := 0
	for _, name := range names {
		row := rows[name]
		if row.doc != "" {
			documented++
		}
		body = append(body, []string{
			"`" + name + "`",
			orDash(row.kind),
			orDash(row.def),
			orDash(row.doc),
		})
	}

	var b strings.Builder
	b.WriteString(heading(1, "环境变量全表"))
	b.WriteString(paragraph(
		"环境变量是**启动时的初始值**。部分设置可以在控制台的「运维 → 运行设置」里热改，"+
			"运行设置会覆盖环境变量而不需要重启。",
		"默认值列来自 `internal/config/config.go` 里的字面量。显示为空表示该变量没有字面量默认值"+
			"（例如 `MASTER_KEY` 必须显式提供）；显示为包限定名（如 `time.Minute`）表示默认值在代码里是计算得出的。",
		"必填项（`ADMIN_TOKEN`、`MASTER_KEY`）由启动校验强制，缺失会让进程直接退出——"+
			"compose 文件刻意没有兜底凭据。",
	))
	b.WriteString(countNote(len(names), "环境变量"))
	b.WriteString(table([]string{"变量", "类型", "默认值", "说明"}, body))
	b.WriteString("\n")
	b.WriteString(heading(2, "怎么读这张表"))
	b.WriteString(bullet([]string{
		"**类型** 列决定值的写法：`逗号分隔列表` 不是 JSON 数组，`整数（秒）` 是秒数而不是 Go duration 字符串。",
		"**说明** 列取自 `Config` 结构体字段的文档注释。本表共 " + itoa(len(names)) + " 个变量，" +
			itoa(documented) + " 个在代码里带注释，其余 " + itoa(len(names)-documented) +
			" 个显示为 `—`——那是代码里的文档缺口，不是生成器失败。",
		"`0` 在多数保留期与阈值项里表示「关闭该功能」，具体见各项说明。",
	}))
	return b.String(), nil
}

// findEnvCall searches an expression for a recognised environment read. It
// exists because Load() wraps readers (`strings.TrimSpace(envStr(…))`,
// `firstNonEmpty(…)`), and requiring the call to be the expression's root would
// silently drop those variables' field and doc bindings.
func findEnvCall(expr ast.Expr) (name, kind, def string, ok bool) {
	ast.Inspect(expr, func(n ast.Node) bool {
		if ok {
			return false
		}
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, kind, def, ok = envCall(call)
		return !ok
	})
	return name, kind, def, ok
}

// envCall extracts (name, kind, default) from any recognised environment read.
func envCall(call *ast.CallExpr) (name, kind, def string, ok bool) {
	if len(call.Args) == 0 {
		return "", "", "", false
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		helperKind, isHelper := envHelperKind[fn.Name]
		if !isHelper {
			return "", "", "", false
		}
		value, isLit := stringLit(call.Args[0])
		if !isLit {
			return "", "", "", false
		}
		if len(call.Args) > 1 {
			if text, isLit := literalText(call.Args[1]); isLit {
				def = text
			}
		}
		return value, helperKind, def, true
	case *ast.SelectorExpr:
		// os.LookupEnv / os.Getenv
		pkg, isIdent := fn.X.(*ast.Ident)
		if !isIdent || pkg.Name != "os" || !directEnvReaders[fn.Sel.Name] {
			return "", "", "", false
		}
		value, isLit := stringLit(call.Args[0])
		if !isLit {
			return "", "", "", false
		}
		return value, "字符串", "", true
	}
	return "", "", "", false
}
