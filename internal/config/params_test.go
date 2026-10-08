package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This test is the guard that keeps the console's deployment-parameter view
// honest. The table in params.go names a Config field per environment variable;
// a wrong field would show the operator a real-looking number that is not the
// one in effect, and nothing else in the build would notice. So the test reads
// config.go and checks both directions: every variable Load reads appears in the
// table bound to the field Load assigns it to, and nothing in the table is dead.

// helperKeys are the readers whose environment variable is not an argument but a
// constant inside the helper.
var helperKeys = map[string][]string{
	"envAdminTokens":         {"ADMIN_TOKEN", "ADMIN_TOKENS"},
	"envModelCatalogSources": {"MODEL_CATALOG_SOURCES"},
}

// sensitiveKey must be masked in the payload. ADMIN_TOKEN_LOGIN deliberately
// does not match: it is a mode string ("break-glass"), not a credential.
var sensitiveKey = regexp.MustCompile(`(?i)(_PASSWORD|_TOKEN|_TOKENS|_KEY)$`)

var alwaysSensitive = map[string]bool{
	"ALERT_CONFIG_JSON": true, // carries SMTP passwords and bot tokens
	"WEBHOOK_URL":       true, // bark/telegram webhook URLs embed their token
}

func TestDeploymentParametersMatchLoad(t *testing.T) {
	keys, fields := envReadsFromSource(t)

	// Direction 1: every variable Load reads is in the table.
	tableKeys := make(map[string]envParamSpec, len(envParamSpecs))
	for _, spec := range envParamSpecs {
		if _, dup := tableKeys[spec.Key]; dup {
			t.Errorf("duplicate entry for %s", spec.Key)
		}
		tableKeys[spec.Key] = spec
	}
	for key := range keys {
		if _, ok := tableKeys[key]; !ok {
			t.Errorf("%s is read in config.go but missing from envParamSpecs — the console would never show it", key)
		}
	}
	// Direction 2: nothing dead, and the field is the one Load assigns.
	configType := reflect.TypeOf(Config{})
	for key, spec := range tableKeys {
		if !keys[key] {
			t.Errorf("envParamSpecs lists %s, which config.go never reads", key)
			continue
		}
		if _, ok := configType.FieldByName(spec.Field); !ok {
			t.Errorf("%s is bound to %s, which is not a Config field", key, spec.Field)
			continue
		}
		if bound := fields[key]; len(bound) > 0 && !bound[spec.Field] {
			t.Errorf("%s is bound to %s, but Load assigns it to %s",
				key, spec.Field, strings.Join(sortedKeys(bound), " / "))
		}
		if want := sensitiveKey.MatchString(key) || alwaysSensitive[key]; want != spec.Secret {
			t.Errorf("%s: Secret=%v, want %v (a credential must never reach the console payload)", key, spec.Secret, want)
		}
	}
}

// envReadsFromSource parses config.go and returns every environment variable it
// reads, plus the Config fields each one's value lands in.
func envReadsFromSource(t *testing.T) (map[string]bool, map[string]map[string]bool) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "config.go", nil, 0)
	if err != nil {
		t.Fatalf("parse config.go: %v", err)
	}

	keys := map[string]bool{}
	// locals maps a local variable to the variables whose value it holds.
	locals := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Load" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 {
				return true
			}
			found := envCalls(assign.Rhs[0])
			if len(found) == 0 {
				return true
			}
			for _, key := range found {
				keys[key] = true
			}
			for _, lhs := range assign.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" || strings.HasSuffix(id.Name, "err") || id.Name == "err" {
					continue
				}
				locals[id.Name] = found
			}
			return true
		})
	}

	// The helper-internal readers are not visible from Load's call sites.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		for _, key := range helperKeys[id.Name] {
			keys[key] = true
		}
		return true
	})

	fields := map[string]map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		field, ok := kv.Key.(*ast.Ident)
		if !ok {
			return true
		}
		// The value is either a local holding the read result, or the read
		// itself (`HTTPAddr: envStr("HTTP_ADDR", ":4100")`).
		bound := envCalls(kv.Value)
		ast.Inspect(kv.Value, func(inner ast.Node) bool {
			if id, ok := inner.(*ast.Ident); ok {
				bound = append(bound, locals[id.Name]...)
			}
			return true
		})
		for _, key := range bound {
			keys[key] = true
			if fields[key] == nil {
				fields[key] = map[string]bool{}
			}
			fields[key][field.Name] = true
		}
		return true
	})
	return keys, fields
}

// envCalls returns the environment variables read anywhere in an expression,
// including nested readers such as strings.TrimSpace(envStr("X", "")).
func envCalls(expr ast.Expr) []string {
	var found []string
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if keys, ok := helperKeys[fn.Name]; ok {
				found = append(found, keys...)
				return true
			}
			if !strings.HasPrefix(fn.Name, "env") || len(call.Args) == 0 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				found = append(found, strings.Trim(lit.Value, `"`))
			}
		case *ast.SelectorExpr:
			pkg, ok := fn.X.(*ast.Ident)
			if !ok || pkg.Name != "os" || (fn.Sel.Name != "Getenv" && fn.Sel.Name != "LookupEnv") {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				found = append(found, strings.Trim(lit.Value, `"`))
			}
		}
		return true
	})
	return found
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// The Secret flag is a promise to the console; this is the test that keeps it.
// ADMIN_TOKENS is a list, so it takes the slice path in the renderer — a path
// that forgot to mask until this test existed.
func TestDeploymentParametersMaskCredentials(t *testing.T) {
	cfg := &Config{
		AdminToken:                 "tok-admin",
		AdminTokens:                []string{"tok-rotate-1", "tok-rotate-2"},
		MasterKey:                  "mk-secret-value",
		MetricsToken:               "metrics-secret-value",
		WebDAVPassword:             "dav-password-value",
		WebDAVBackupPassword:       "dav-backup-value",
		WebDAVUploadPassword:       "dav-upload-value",
		WebDAVUploadBackupPassword: "dav-upload-backup-value",
		AlertConfigJSON:            `{"smtp":{"password":"smtp-password-value"}}`,
		WebhookURL:                 "https://api.telegram.org/bot-token-value/send",
		WebDAVURL:                  "https://dav-user:dav-userinfo-value@dav.example.com/dav",
		OutboundImageHeaderTimeout: 5 * time.Minute,
	}

	rendered := map[string]string{}
	for _, param := range cfg.DeploymentParameters() {
		rendered[param.Key] = param.Value
	}
	secrets := []string{
		"tok-admin", "tok-rotate-1", "tok-rotate-2", "mk-secret-value", "metrics-secret-value",
		"dav-password-value", "dav-backup-value", "dav-upload-value", "dav-upload-backup-value",
		"smtp-password-value", "bot-token-value", "dav-userinfo-value",
	}
	for _, secret := range secrets {
		for key, value := range rendered {
			if strings.Contains(value, secret) {
				t.Errorf("%s rendered %q — a credential reached the console payload", key, secret)
			}
		}
	}
	// Masked, not hidden: the operator still learns that something is configured.
	if got := rendered["ADMIN_TOKENS"]; got == "" {
		t.Error("ADMIN_TOKENS renders empty for a configured list, so the console cannot tell set from unset")
	}
	// A URL keeps its host — that is what makes the row useful — and loses the
	// credentials that may be embedded in it.
	if got := rendered["WEBDAV_URL"]; !strings.Contains(got, "dav.example.com") || strings.Contains(got, "dav-user") {
		t.Errorf("WEBDAV_URL = %q, want the host with the userinfo masked", got)
	}
	if got := rendered["OUTBOUND_IMAGE_HEADER_TIMEOUT_SECONDS"]; got != "5m0s" {
		t.Errorf("duration rendering = %q, want 5m0s", got)
	}
}
