package api

import (
	"fmt"
	"net/http"
	"strings"

	"cmspro-local-service/internal/config"
)

// AuthMiddleware 认证中间件，校验 Bearer Token
// 配置的 AuthToken 为空时跳过认证（开发模式）
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := config.Get().AuthToken
		if token == "" {
			// 无 token 配置时跳过认证（开发模式）
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, 401, "缺少认证信息")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] != token {
			writeError(w, 401, "认证失败")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// OpenAPIAuthMiddleware 开放 API 认证中间件
// 校验 X-API-Key 头部，仅在 OpenAPIEnabled=true 且 OpenAPIKey 非空时生效
func OpenAPIAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := config.Get()
		if !cfg.OpenAPIEnabled || cfg.OpenAPIKey == "" {
			writeError(w, 403, "开放 API 未启用")
			return
		}

		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			writeError(w, 401, "缺少 API Key")
			return
		}
		if apiKey != cfg.OpenAPIKey {
			writeError(w, 401, "API Key 无效")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeError 输出统一错误响应
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write([]byte(fmt.Sprintf(`{"code":%d,"message":"%s","data":null}`, code, message)))
}
