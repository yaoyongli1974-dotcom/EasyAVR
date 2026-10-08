// Package openapi describes EasyAVR's third-party/APP open API and bounds its
// usage with a per-key rate limiter.
package openapi

import "encoding/json"

// BasePath is the prefix of every open API endpoint.
const BasePath = "/api/v1/open"

// Spec renders the OpenAPI 3.0 document for the open API.
func Spec() ([]byte, error) {
	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "EasyAVR 开放 API",
			"version":     "1.0.0",
			"description": "面向 APP 端与第三方系统的开放接口。使用 X-API-Key 请求头（或 Authorization: ApiKey <secret>）鉴权；访问范围由密钥的 read/write 权限决定。",
		},
		"servers":  []any{map[string]any{"url": BasePath}},
		"security": []any{map[string]any{"ApiKeyAuth": []any{}}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"ApiKeyAuth": map[string]any{
					"type": "apiKey", "in": "header", "name": "X-API-Key",
				},
			},
			"schemas": map[string]any{
				"ApiResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code":    map[string]any{"type": "integer", "example": 0},
						"message": map[string]any{"type": "string", "example": "ok"},
						"data":    map[string]any{"type": "object"},
					},
				},
				"AIEvent": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "integer"},
						"channelId":  map[string]any{"type": "integer"},
						"kind":       map[string]any{"type": "string", "example": "cv"},
						"eventType":  map[string]any{"type": "string", "example": "person_intrusion"},
						"level":      map[string]any{"type": "string", "enum": []string{"info", "warning", "critical"}},
						"confidence": map[string]any{"type": "number"},
						"summary":    map[string]any{"type": "string"},
						"payload":    map[string]any{"type": "string"},
						"occurredAt": map[string]any{"type": "string", "format": "date-time"},
					},
				},
			},
		},
		"paths": map[string]any{
			"/devices": map[string]any{
				"get": op("列出设备", "read", "分页返回设备（含通道）。", nil, response("设备分页")),
			},
			"/channels": map[string]any{
				"get": op("列出通道", "read", "返回全部可播放通道。", query("keyword", "在线状态过滤需用 online=true"), response("通道列表")),
			},
			"/channels/{id}/play-urls": map[string]any{
				"get": op("获取通道播放地址", "read", "返回 FLV/HLS/RTSP 等播放地址。", pathID(), response("播放地址")),
			},
			"/resources": map[string]any{
				"get": op("列出视频资源", "read", "视频资源中心的实时/录像/快照编目。", nil, response("资源分页")),
			},
			"/events": map[string]any{
				"get":  op("查询 AI 事件", "read", "支持 kind/level/eventType/keyword/from/to 过滤。", nil, response("事件分页")),
				"post": op("上报 AI 事件", "write", "外部 AI worker/APP 上报事件，进入事件中心并触发通知与索引。", body("AIEvent"), response("已创建事件")),
			},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func op(summary, scope, desc string, params any, resp any) map[string]any {
	m := map[string]any{
		"summary":     summary,
		"description": desc + "（需要 " + scope + " 权限）",
		"responses":   map[string]any{"200": resp, "401": errResp("缺少或无效的 API Key"), "403": errResp("权限不足"), "429": errResp("超出速率或配额限制")},
	}
	if params != nil {
		m["parameters"] = params
	}
	return m
}

func query(name, desc string) []any {
	return []any{
		map[string]any{"name": name, "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": desc},
		map[string]any{"name": "page", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "default": 1}},
		map[string]any{"name": "pageSize", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "default": 20}},
	}
}

func pathID() []any {
	return []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}}}
}

func body(ref string) map[string]any {
	return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + ref}}}}
}

func response(desc string) map[string]any {
	return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ApiResult"}}}}
}

func errResp(desc string) map[string]any {
	return map[string]any{"description": desc}
}
