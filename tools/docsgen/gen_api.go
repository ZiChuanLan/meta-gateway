package main

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strings"
)

// httpMethods are the chi registration verbs. Only a call shaped
// `r.<Verb>("<path>", handler)` counts as a route registration, which keeps
// unrelated calls such as `r.Header.Get("X")` out of the table.
var httpMethods = map[string]bool{
	"Get":    true,
	"Post":   true,
	"Put":    true,
	"Patch":  true,
	"Delete": true,
}

type apiRow struct {
	method  string
	path    string
	handler string
	file    string
}

func genAdminAPI(root string) (string, error) {
	rows, err := collectRoutes(root, false)
	if err != nil {
		return "", err
	}
	return renderAPI(rows, apiRenderOptions{
		title:  "管理 API 全表",
		prefix: "/admin",
		intro: []string{
			"管理面端点注册在 chi 的 admin 子路由上，挂载点是 `/admin`，所以表中的路径都要加上该前缀。",
			"鉴权：`Authorization: Bearer <session_token>`，由 `POST /admin/session` 换取。" +
				"`/admin/session` 本身与少数公开入口除外。",
			"除特别说明外，请求与响应都是 JSON；请求体上限由 `MAX_ADMIN_BODY_BYTES` 约束。",
		},
		groupByPath: true,
	}), nil
}

func genPublicAPI(root string) (string, error) {
	rows, err := collectRoutes(root, true)
	if err != nil {
		return "", err
	}
	return renderAPI(rows, apiRenderOptions{
		title:  "公开接口全表",
		prefix: "/v1",
		intro: []string{
			"下游端点注册在 chi 的 v1 子路由上，挂载点是 `/v1`，所以表中的路径都要加上该前缀。",
			"鉴权：`Authorization: Bearer <下游令牌>`（OpenAI 风格）或 `x-api-key`（Anthropic 风格客户端）。" +
				"每个端点要求对应的 scope，缺少时返回 `403 insufficient scope`。",
			"**未登记的 `/v1` 路径由兜底路由原样透传**（路径需通过闭集白名单），它不在这张表里。",
		},
		groupByPath: false,
	}), nil
}

type apiRenderOptions struct {
	title       string
	prefix      string
	intro       []string
	groupByPath bool
}

func collectRoutes(root string, public bool) ([]apiRow, error) {
	dir := filepath.Join(root, "internal", "httpapi")
	files, err := goFilesIn(dir)
	if err != nil {
		return nil, err
	}
	var rows []apiRow
	for _, name := range files {
		// relay*.go registers the /v1 surface; everything else registers on the
		// admin group (or a sub-group of it, as the WebDAV handler does).
		isPublic := strings.HasPrefix(name, "relay")
		if isPublic != public {
			continue
		}
		file, _, err := parseGoFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != "r" || !httpMethods[sel.Sel.Name] {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			path, ok := stringLit(call.Args[0])
			if !ok {
				return true
			}
			handler := ""
			if len(call.Args) > 1 {
				handler = exprText(call.Args[1])
				if idx := strings.LastIndexByte(handler, '.'); idx >= 0 {
					handler = handler[idx+1:]
				}
			}
			rows = append(rows, apiRow{method: sel.Sel.Name, path: path, handler: handler, file: name})
			return true
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		return rows[i].method < rows[j].method
	})
	return rows, nil
}

func renderAPI(rows []apiRow, opts apiRenderOptions) string {
	var b strings.Builder
	b.WriteString(heading(1, opts.title))
	b.WriteString(paragraph(opts.intro...))

	// Count distinct paths and methods for an honest size statement.
	paths := map[string]bool{}
	methods := map[string]bool{}
	for _, row := range rows {
		paths[row.path] = true
		methods[row.method] = true
	}
	b.WriteString(paragraph(
		"共 **" + itoa(len(rows)) + "** 条注册、**" + itoa(len(paths)) + "** 个路径、" +
			joinOrDash(sortedKeys(methods)) + " 方法。",
	))

	if opts.groupByPath {
		byFirstSegment := map[string][]apiRow{}
		var order []string
		for _, row := range rows {
			segment := firstSegment(row.path)
			if _, seen := byFirstSegment[segment]; !seen {
				order = append(order, segment)
			}
			byFirstSegment[segment] = append(byFirstSegment[segment], row)
		}
		sortStrings(order)
		for _, segment := range order {
			group := byFirstSegment[segment]
			b.WriteString(heading(2, opts.prefix+"/"+segment+" · "+itoa(len(group))+" 条"))
			var body [][]string
			for _, row := range group {
				body = append(body, []string{
					"`" + row.method + "`",
					"`" + opts.prefix + row.path + "`",
					orDash(row.handler),
				})
			}
			b.WriteString(table([]string{"方法", "路径", "handler"}, body))
			b.WriteString("\n")
		}
		return b.String()
	}

	var body [][]string
	for _, row := range rows {
		body = append(body, []string{
			"`" + row.method + "`",
			"`" + opts.prefix + row.path + "`",
			orDash(row.handler),
		})
	}
	b.WriteString(table([]string{"方法", "路径", "handler"}, body))
	b.WriteString("\n")
	return b.String()
}

func firstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		return trimmed[:idx]
	}
	if trimmed == "" {
		return "(root)"
	}
	return trimmed
}
