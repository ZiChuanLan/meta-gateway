package main

import (
	"path/filepath"
	"strings"
)

func genRuntimeSettings(root string) (string, error) {
	path := filepath.Join(root, "internal", "runtimeconfig", "runtimeconfig.go")
	file, _, err := parseGoFile(path)
	if err != nil {
		return "", err
	}
	st, ok := findStruct(file, "Editable")
	if !ok {
		return "", nil
	}

	var rows [][]string
	keys := 0
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded or anonymous field
		}
		key := jsonTagName(field.Tag)
		if key == "" {
			continue
		}
		keys++
		doc := docText(field.Doc)
		if doc == "" {
			doc = docText(field.Comment)
		}
		rows = append(rows, []string{
			"`" + key + "`",
			field.Names[0].Name,
			exprText(field.Type),
			orDash(doc),
		})
	}

	var b strings.Builder
	b.WriteString(heading(1, "运行设置全表"))
	b.WriteString(paragraph(
		"运行设置存在 `runtime_settings` 表里，由控制台的「运维 → 运行设置」编辑。它们**覆盖**环境变量，"+
			"多数改动热生效，不需要重启进程。",
		"「字段」列是 Go 结构体字段名，用于在代码里定位该设置；「键」列是持久化与 API 使用的名字。",
		"没有出现在这张表里的开关只存在于环境变量层——想彻底关掉某个后台任务，两处都要看。",
	))
	b.WriteString(countNote(keys, "运行设置键"))
	b.WriteString(table([]string{"键", "字段", "类型", "说明"}, rows))
	b.WriteString("\n")
	b.WriteString(heading(2, "环境变量与运行设置的关系"))
	b.WriteString(bullet([]string{
		"进程启动时，环境变量是**初始值**；控制台保存的值成为**覆盖**，优先于环境变量。",
		"清空一个覆盖（而不是设成 0）才会回落到环境变量值——`0` 通常是一个有意义的取值（关闭）。",
		"热生效的边界由各项的 applier 决定：涉及重建后台循环的改动会在下一个周期生效。",
	}))
	return b.String(), nil
}
