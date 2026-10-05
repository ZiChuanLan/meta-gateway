package main

import (
	"strings"

	"github.com/lan/meta-gateway/internal/proxy"
)

// genProviderProfiles enumerates the real registry through proxy.Profiles()
// rather than parsing the source, so a profile added to the registry shows up
// here without a second edit.
func genProviderProfiles(root string) (string, error) {
	profiles := proxy.Profiles()

	var b strings.Builder
	b.WriteString(heading(1, "供应商 profile 全表"))
	b.WriteString(paragraph(
		"非 OpenAI 供应商的端点与字段映射是**供应商的属性**，所以随供应商一起发布，而不是控制台里一个"+
			"需要操作员点击、再手工验证的预设按钮。",
		"保存渠道时 `ApplyProviderProfile` 按 `type_hint`（留空取 `site.platform`）查表，**只填空位**："+
			"已经手写请求 / 响应映射的渠道完全不动。路径覆盖是这条闸门的豁免项——"+
			"「把完整端点 URL 粘进 base_url」写的就是那个字段，属同一意图。",
		"profile 自己会先过一遍 `ValidateUpstreamMap`，所以不可能存进一份非法映射。",
	))

	b.WriteString(countNote(len(profiles), "profile"))
	var rows [][]string
	for _, profile := range profiles {
		rows = append(rows, []string{
			"`" + profile.Type + "`",
			joinOrDash(backtickAll(profile.Aliases)),
			orDash(codeOrDash(profile.PathOverride)),
			joinOrDash(backtickAll(profile.ReasoningLevels)),
			joinOrDash(backtickAll(profile.MatchPaths)),
		})
	}
	b.WriteString(table([]string{"类型", "别名", "路径覆盖", "接受的推理档位", "端点匹配"}, rows))
	b.WriteString("\n")

	b.WriteString(heading(2, "运行时能力与保存期映射是两件事"))
	b.WriteString(bullet([]string{
		"**`接受的推理档位`** 是运行时能力：转发前把下游的 `reasoning_effort` 落到上游真认的档位，" +
			"否则上游会直接 400。查表先按 `type_hint`，查不到再按端点后缀匹配。",
		"**保存期的字段映射刻意不适用端点匹配。** 手写映射的主人是操作员，运行时只借能力、不碰映射。",
		"操作员声明的天花板 `max_reasoning_effort` 与 profile 的能力**独立生效**：" +
			"留空或选 `max` 表示不降档。改写会记录在 `mapped_reasoning_effort` 上（如 `max→xhigh`），" +
			"**降档不消耗故障转移预算**。",
	}))

	for _, profile := range profiles {
		if profile.RequestMap == "" && profile.ResponseMap == "" {
			continue
		}
		b.WriteString(heading(2, "`"+profile.Type+"` 的映射"))
		if profile.PathOverride != "" {
			b.WriteString(paragraph("路径覆盖：`" + profile.PathOverride + "`"))
		}
		if profile.RequestMap != "" {
			b.WriteString(heading(3, "请求映射"))
			b.WriteString(codeBlock("json", profile.RequestMap))
		}
		if profile.ResponseMap != "" {
			b.WriteString(heading(3, "响应映射"))
			b.WriteString(codeBlock("json", profile.ResponseMap))
		}
	}

	b.WriteString(heading(2, "新增一个非 OpenAI 供应商要动四处"))
	b.WriteString(bullet([]string{
		"Go 的 `OpenAICompatibleBrands()` + `CanonicalType`（映射到 `openai-compatible`，让模型清单与转发解析命中）；",
		"`providerProfiles` 注册 profile；",
		"前端 `connectionTypes.ts` 的 `CONNECTION_TYPE_OPTIONS` 与 base URL 预设；",
		"前端 `helpers.tsx` 的 `NO_USER_AUTH_TYPES`（否则抽屉里会多出无用的用户令牌字段）。",
	}))

	return b.String(), nil
}

func backtickAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+value+"`")
	}
	return out
}

func codeOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "`" + value + "`"
}
